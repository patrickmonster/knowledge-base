package kb

import (
	"context"
	"database/sql"
	"sort"
	"strings"
)

// Context usage feedback (start_task 맥락 활용도).
//
// After a task, the agent records which documents it actually relied on.
// Offered sources that were not named become "unused". The ratios across
// tasks measure retrieval quality: how much of what start_task offers is used
// (precision), how much of what was used had been offered (coverage), and
// which documents are frequently offered but never used.

const maxFeedbackSources = 50

type ContextFeedbackInput struct {
	ProjectID         string   `json:"project_id"`
	TaskID            string   `json:"task_id"`
	UsedSources       []string `json:"used_sources" jsonschema:"Document IDs actually relied on as evidence, whether start_task offered them or you found them later; at most 50"`
	MisleadingSources []string `json:"misleading_sources,omitempty" jsonschema:"Document IDs that were wrong or outdated for this task; at most 50"`
	Missing           string   `json:"missing,omitempty" jsonschema:"Knowledge you needed but could not find, up to 2000 characters"`
}

type ContextFeedbackResult struct {
	TaskID         string   `json:"task_id"`
	Offered        int      `json:"offered"`
	Used           int      `json:"used"`
	UsedOffered    int      `json:"used_offered"`
	UsedNotOffered int      `json:"used_not_offered"`
	Misleading     int      `json:"misleading"`
	Unused         int      `json:"unused"`
	Precision      *float64 `json:"precision"`
}

func uniqueIDs(label string, ids []string) ([]string, error) {
	if len(ids) > maxFeedbackSources {
		return nil, problem("INVALID_INPUT", "At most 50 "+label+" are allowed.")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, id := range ids {
		if err := validID(id); err != nil {
			return nil, err
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// offeredSources returns start_task source document IDs with their 1-based rank.
func offeredSources(run TaskRun) map[string]int {
	out := map[string]int{}
	for i, s := range run.Context.Sources {
		if _, ok := out[s.DocumentID]; !ok {
			out[s.DocumentID] = i + 1
		}
	}
	return out
}

// recordContextFeedback replaces the feedback of one task. The caller must
// hold the task row lock and have checked that the actor started the task.
func recordContextFeedback(ctx context.Context, tx *sql.Tx, a Identity, run TaskRun, in ContextFeedbackInput) (ContextFeedbackResult, error) {
	out := ContextFeedbackResult{TaskID: run.ID}
	used, err := uniqueIDs("used_sources", in.UsedSources)
	if err != nil {
		return out, err
	}
	misleading, err := uniqueIDs("misleading_sources", in.MisleadingSources)
	if err != nil {
		return out, err
	}
	in.Missing = strings.TrimSpace(in.Missing)
	if err := bounded("missing", in.Missing, 0, 2000); err != nil {
		return out, err
	}
	verdicts := map[string]string{}
	for _, id := range used {
		verdicts[id] = "used"
	}
	for _, id := range misleading {
		if verdicts[id] != "" {
			return out, problem("INVALID_INPUT", "A document cannot be both used and misleading: "+id)
		}
		verdicts[id] = "misleading"
	}
	if len(verdicts) > 0 {
		ids := make([]string, 0, len(verdicts))
		for id := range verdicts {
			ids = append(ids, id)
		}
		var found int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM documents WHERE project_id=$1 AND id::text=ANY($2::text[])`, run.ProjectID, ids).Scan(&found); err != nil {
			return out, err
		}
		if found != len(ids) {
			return out, problem("NOT_FOUND", "Every feedback document must exist in this project.")
		}
	}
	offered := offeredSources(run)
	for id := range offered {
		if verdicts[id] == "" {
			verdicts[id] = "unused"
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM task_source_feedback WHERE task_id=$1`, run.ID); err != nil {
		return out, err
	}
	ids := make([]string, 0, len(verdicts))
	for id := range verdicts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rank, wasOffered := offered[id]
		var rankValue any
		if wasOffered {
			rankValue = rank
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_source_feedback(task_id,document_id,project_id,verdict,offered,rank,created_by) VALUES($1,$2,$3,$4,$5,$6,$7)`,
			run.ID, id, run.ProjectID, verdicts[id], wasOffered, rankValue, a.User.ID); err != nil {
			return out, err
		}
		switch verdicts[id] {
		case "used":
			out.Used++
			if wasOffered {
				out.UsedOffered++
			} else {
				out.UsedNotOffered++
			}
		case "misleading":
			out.Misleading++
		default:
			out.Unused++
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE task_runs SET feedback_note=$2,feedback_at=clock_timestamp() WHERE id=$1`, run.ID, in.Missing); err != nil {
		return out, err
	}
	out.Offered = len(offered)
	if out.Offered > 0 {
		p := float64(out.UsedOffered) / float64(out.Offered)
		out.Precision = &p
	}
	return out, nil
}

func (s *Store) recordContextFeedbackTool(ctx context.Context, tx *sql.Tx, a Identity, in ContextFeedbackInput) (any, error) {
	if _, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, true); err != nil {
		return nil, err
	}
	if err := validID(in.TaskID); err != nil {
		return nil, err
	}
	run, err := one[TaskRun](ctx, tx, `SELECT to_jsonb(r)-'completion_hash' FROM task_runs r WHERE id=$1 AND project_id=$2 FOR UPDATE`, in.TaskID, in.ProjectID)
	if err != nil {
		return nil, notFound(err)
	}
	if run.CreatedBy != a.User.ID {
		return nil, problem("FORBIDDEN", "Only the user who started this task may record its feedback.")
	}
	return recordContextFeedback(ctx, tx, a, run, in)
}

type RetrievalMetricsInput struct {
	ProjectID string `json:"project_id"`
	Days      int    `json:"days,omitempty" jsonschema:"Look-back window in days, 1–365; default 30"`
}

type DocumentUsage struct {
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Offered    int    `json:"offered"`
	Used       int    `json:"used"`
	Misleading int    `json:"misleading"`
}

type RetrievalMetrics struct {
	ProjectID          string          `json:"project_id"`
	Days               int             `json:"days"`
	TasksStarted       int             `json:"tasks_started"`
	TasksWithFeedback  int             `json:"tasks_with_feedback"`
	Offered            int             `json:"offered"`
	OfferedUsed        int             `json:"offered_used"`
	UsedTotal          int             `json:"used_total"`
	UsedNotOffered     int             `json:"used_not_offered"`
	Misleading         int             `json:"misleading"`
	Precision          *float64        `json:"precision" jsonschema:"offered_used / offered"`
	Coverage           *float64        `json:"coverage" jsonschema:"offered_used / used_total: share of used evidence that start_task had offered"`
	HitRate            *float64        `json:"hit_rate" jsonschema:"Share of tasks with feedback where at least one offered source was used"`
	MeanReciprocalRank *float64        `json:"mean_reciprocal_rank" jsonschema:"Mean of 1/rank of the best-ranked used source per task; 0 when none was used"`
	MostUsed           []DocumentUsage `json:"most_used"`
	OfferedNeverUsed   []DocumentUsage `json:"offered_never_used"`
	MisleadingSources  []DocumentUsage `json:"misleading_sources"`
	RecentMissing      []string        `json:"recent_missing"`
	Guidance           string          `json:"guidance"`
}

func ratio(n, d int) *float64 {
	if d == 0 {
		return nil
	}
	r := float64(int(float64(n)/float64(d)*1000+0.5)) / 1000
	return &r
}

func retrievalMetrics(ctx context.Context, tx *sql.Tx, a Identity, in RetrievalMetricsInput) (any, error) {
	if _, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false); err != nil {
		return nil, err
	}
	if in.Days == 0 {
		in.Days = 30
	}
	if in.Days < 1 || in.Days > 365 {
		return nil, problem("INVALID_INPUT", "days must be 1–365.")
	}
	out := RetrievalMetrics{ProjectID: in.ProjectID, Days: in.Days, MostUsed: []DocumentUsage{}, OfferedNeverUsed: []DocumentUsage{}, MisleadingSources: []DocumentUsage{}, RecentMissing: []string{},
		Guidance: "Low precision with many offered_never_used documents suggests noisy sources: archive, supersede or retitle them. Low coverage means evidence exists that start_task does not find: add glossary synonyms or better titles/tags. Read recent_missing for knowledge to write."}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM task_runs WHERE project_id=$1 AND started_at>=now()-make_interval(days=>$2::int)`, in.ProjectID, in.Days).Scan(&out.TasksStarted); err != nil {
		return nil, err
	}
	type row struct {
		TaskID     string `json:"task_id"`
		DocumentID string `json:"document_id"`
		Verdict    string `json:"verdict"`
		Offered    bool   `json:"offered"`
		Rank       *int   `json:"rank"`
	}
	rows, err := many[row](ctx, tx, `SELECT jsonb_build_object('task_id',f.task_id,'document_id',f.document_id,'verdict',f.verdict,'offered',f.offered,'rank',f.rank)
	FROM task_source_feedback f JOIN task_runs r ON r.id=f.task_id WHERE f.project_id=$1 AND r.feedback_at>=now()-make_interval(days=>$2::int) LIMIT 20000`, in.ProjectID, in.Days)
	if err != nil {
		return nil, err
	}
	type taskStat struct{ bestRank int }
	tasks := map[string]*taskStat{}
	docs := map[string]*DocumentUsage{}
	for _, r := range rows {
		t := tasks[r.TaskID]
		if t == nil {
			t = &taskStat{}
			tasks[r.TaskID] = t
		}
		d := docs[r.DocumentID]
		if d == nil {
			d = &DocumentUsage{DocumentID: r.DocumentID}
			docs[r.DocumentID] = d
		}
		if r.Offered {
			out.Offered++
			d.Offered++
		}
		switch r.Verdict {
		case "used":
			out.UsedTotal++
			d.Used++
			if r.Offered {
				out.OfferedUsed++
				if r.Rank != nil && (t.bestRank == 0 || *r.Rank < t.bestRank) {
					t.bestRank = *r.Rank
				}
			} else {
				out.UsedNotOffered++
			}
		case "misleading":
			out.Misleading++
			d.Misleading++
		}
	}
	out.TasksWithFeedback = len(tasks)
	hits := 0
	reciprocal := 0.0
	for _, t := range tasks {
		if t.bestRank > 0 {
			hits++
			reciprocal += 1 / float64(t.bestRank)
		}
	}
	out.Precision = ratio(out.OfferedUsed, out.Offered)
	out.Coverage = ratio(out.OfferedUsed, out.UsedTotal)
	out.HitRate = ratio(hits, len(tasks))
	if len(tasks) > 0 {
		mrr := float64(int(reciprocal/float64(len(tasks))*1000+0.5)) / 1000
		out.MeanReciprocalRank = &mrr
	}
	if len(docs) > 0 {
		ids := make([]string, 0, len(docs))
		for id := range docs {
			ids = append(ids, id)
		}
		type meta struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Kind  string `json:"kind"`
		}
		metas, err := many[meta](ctx, tx, `SELECT jsonb_build_object('id',id,'title',title,'kind',kind) FROM documents WHERE project_id=$1 AND id::text=ANY($2::text[])`, in.ProjectID, ids)
		if err != nil {
			return nil, err
		}
		for _, m := range metas {
			docs[m.ID].Title, docs[m.ID].Kind = m.Title, m.Kind
		}
	}
	list := make([]DocumentUsage, 0, len(docs))
	for _, d := range docs {
		list = append(list, *d)
	}
	pick := func(keep func(DocumentUsage) bool, less func(a, b DocumentUsage) bool) []DocumentUsage {
		picked := []DocumentUsage{}
		for _, d := range list {
			if keep(d) {
				picked = append(picked, d)
			}
		}
		sort.Slice(picked, func(i, j int) bool {
			if less(picked[i], picked[j]) != less(picked[j], picked[i]) {
				return less(picked[i], picked[j])
			}
			return picked[i].DocumentID < picked[j].DocumentID
		})
		if len(picked) > 10 {
			picked = picked[:10]
		}
		return picked
	}
	out.MostUsed = pick(func(d DocumentUsage) bool { return d.Used > 0 }, func(a, b DocumentUsage) bool { return a.Used > b.Used })
	out.OfferedNeverUsed = pick(func(d DocumentUsage) bool { return d.Offered >= 2 && d.Used == 0 }, func(a, b DocumentUsage) bool { return a.Offered > b.Offered })
	out.MisleadingSources = pick(func(d DocumentUsage) bool { return d.Misleading > 0 }, func(a, b DocumentUsage) bool { return a.Misleading > b.Misleading })
	if out.RecentMissing, err = many[string](ctx, tx, `SELECT to_jsonb(feedback_note) FROM task_runs WHERE project_id=$1 AND feedback_note<>''
	AND feedback_at>=now()-make_interval(days=>$2::int) ORDER BY feedback_at DESC LIMIT 10`, in.ProjectID, in.Days); err != nil {
		return nil, err
	}
	return out, nil
}
