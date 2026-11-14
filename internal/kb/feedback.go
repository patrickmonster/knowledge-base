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
	// Every count is aggregated in SQL over the whole window. A task with
	// feedback but no source rows (nothing offered, nothing used) still counts
	// in the task denominators with a hit and reciprocal rank of 0.
	totals, err := one[metricTotals](ctx, tx, feedbackWindow+`, best AS (
		SELECT t.id, min(f.rank) FILTER (WHERE f.verdict='used' AND f.offered) AS r FROM t LEFT JOIN f ON f.task_id=t.id GROUP BY t.id)
	SELECT jsonb_build_object('tasks',(SELECT count(*) FROM t),
		'offered',(SELECT count(*) FROM f WHERE offered),
		'offered_used',(SELECT count(*) FROM f WHERE offered AND verdict='used'),
		'used_total',(SELECT count(*) FROM f WHERE verdict='used'),
		'used_not_offered',(SELECT count(*) FROM f WHERE NOT offered AND verdict='used'),
		'misleading',(SELECT count(*) FROM f WHERE verdict='misleading'),
		'hits',(SELECT count(*) FROM best WHERE r IS NOT NULL),
		'reciprocal',(SELECT coalesce(sum(1.0/r),0) FROM best WHERE r IS NOT NULL))`, in.ProjectID, in.Days)
	if err != nil {
		return nil, err
	}
	out.TasksWithFeedback, out.Offered, out.OfferedUsed = totals.Tasks, totals.Offered, totals.OfferedUsed
	out.UsedTotal, out.UsedNotOffered, out.Misleading = totals.UsedTotal, totals.UsedNotOffered, totals.Misleading
	out.Precision = ratio(out.OfferedUsed, out.Offered)
	out.Coverage = ratio(out.OfferedUsed, out.UsedTotal)
	out.HitRate = ratio(totals.Hits, totals.Tasks)
	if totals.Tasks > 0 {
		mrr := float64(int(totals.Reciprocal/float64(totals.Tasks)*1000+0.5)) / 1000
		out.MeanReciprocalRank = &mrr
	}
	// Only the displayed document lists are limited, never the aggregation.
	for _, list := range []struct {
		target *[]DocumentUsage
		where  string
		order  string
	}{
		{&out.MostUsed, "g.used>0", "g.used DESC"},
		{&out.OfferedNeverUsed, "g.offered>=2 AND g.used=0", "g.offered DESC"},
		{&out.MisleadingSources, "g.misleading>0", "g.misleading DESC"},
	} {
		if *list.target, err = many[DocumentUsage](ctx, tx, feedbackWindow+`, g AS (
			SELECT document_id,count(*) FILTER (WHERE offered) AS offered,count(*) FILTER (WHERE verdict='used') AS used,
			count(*) FILTER (WHERE verdict='misleading') AS misleading FROM f GROUP BY document_id)
		SELECT jsonb_build_object('document_id',d.id,'title',d.title,'kind',d.kind,'offered',g.offered,'used',g.used,'misleading',g.misleading)
		FROM g JOIN documents d ON d.id=g.document_id WHERE `+list.where+` ORDER BY `+list.order+`,d.id LIMIT 10`, in.ProjectID, in.Days); err != nil {
			return nil, err
		}
	}
	if out.RecentMissing, err = many[string](ctx, tx, `SELECT to_jsonb(feedback_note) FROM task_runs WHERE project_id=$1 AND feedback_note<>''
	AND feedback_at>=now()-make_interval(days=>$2::int) ORDER BY feedback_at DESC LIMIT 10`, in.ProjectID, in.Days); err != nil {
		return nil, err
	}
	return out, nil
}

// feedbackWindow selects tasks with feedback recorded in the window (t) and
// their source rows (f). Parameters: $1 project ID, $2 days.
const feedbackWindow = `WITH t AS (SELECT id FROM task_runs WHERE project_id=$1 AND feedback_at>=now()-make_interval(days=>$2::int)),
	f AS (SELECT f.* FROM task_source_feedback f JOIN t ON t.id=f.task_id)`

type metricTotals struct {
	Tasks          int     `json:"tasks"`
	Offered        int     `json:"offered"`
	OfferedUsed    int     `json:"offered_used"`
	UsedTotal      int     `json:"used_total"`
	UsedNotOffered int     `json:"used_not_offered"`
	Misleading     int     `json:"misleading"`
	Hits           int     `json:"hits"`
	Reciprocal     float64 `json:"reciprocal"`
}
