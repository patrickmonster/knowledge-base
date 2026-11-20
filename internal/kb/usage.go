package kb

import (
	"context"
	"database/sql"
	"time"
)

// Usage statistics (사용 통계 대시보드).
//
// One read-only call aggregates how a project is being used over a look-back
// window: tool calls from the audit log, document writes from revisions, task
// runs, searches and reads, and the current size of the knowledge. Days are
// bucketed in Asia/Seoul so the daily series matches how the team reads dates.
// Audit-based numbers only cover what AUDIT_RETENTION_DAYS keeps.

const usageTimezone = "Asia/Seoul"

// Audit actions counted as searches and document reads.
var (
	usageSearchActions = []string{"search_knowledge", "get_task_context", "start_task"}
	usageReadActions   = []string{"get_document"}
)

type UsageStatsInput struct {
	ProjectID string `json:"project_id"`
	Days      int    `json:"days,omitempty" jsonschema:"Look-back window in days, 1–365; default 30"`
}

type UsageCount struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

type UsageDay struct {
	Date           string `json:"date" jsonschema:"YYYY-MM-DD in Asia/Seoul"`
	Calls          int    `json:"calls"`
	Searches       int    `json:"searches"`
	DocumentWrites int    `json:"document_writes"`
	TasksStarted   int    `json:"tasks_started"`
	ActiveUsers    int    `json:"active_users"`
}

type UsageUser struct {
	UserID         string     `json:"user_id"`
	Name           string     `json:"name"`
	Calls          int        `json:"calls"`
	DocumentWrites int        `json:"document_writes"`
	TasksStarted   int        `json:"tasks_started"`
	LastActiveAt   *time.Time `json:"last_active_at"`
}

type UsageDocument struct {
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	Reads      int    `json:"reads"`
}

type UsageSearchMiss struct {
	Query  string    `json:"query"`
	Count  int       `json:"count"`
	LastAt time.Time `json:"last_at"`
}

// UsageSearchLatency summarizes search_timings for one retrieval path.
// Phase averages and percentiles are null when the phase never ran.
type UsageSearchLatency struct {
	Retrieval        string    `json:"retrieval" jsonschema:"Retrieval actually used: hybrid, semantic, lexical or lexical-fallback"`
	Searches         int       `json:"searches"`
	P50MS            float64   `json:"p50_ms" jsonschema:"Median total search time in milliseconds"`
	P95MS            float64   `json:"p95_ms"`
	MaxMS            float64   `json:"max_ms"`
	AvgEmbedMS       *float64  `json:"avg_embed_ms" jsonschema:"Average query embedding time (Ollama)"`
	P95VectorMS      *float64  `json:"p95_vector_ms" jsonschema:"95th percentile exact vector scan time; the figure an HNSW index would reduce"`
	AvgLexicalMS     *float64  `json:"avg_lexical_ms"`
	MaxScannedChunks int       `json:"max_scanned_chunks" jsonschema:"Most embedded chunks one exact vector scan compared"`
	LastAt           time.Time `json:"last_at"`
}

// UsageKindGrowth is the document count of one kind over the window. Kinds are
// the documents' current kind; archived documents still count.
type UsageKindGrowth struct {
	Kind       string `json:"kind"`
	Start      int    `json:"start" jsonschema:"Documents of this kind created before the window"`
	Added      int    `json:"added"`
	Total      int    `json:"total"`
	Cumulative []int  `json:"cumulative" jsonschema:"Count at the end of each day of the window, aligned with daily"`
}

type UsageTotals struct {
	Calls          int `json:"calls" jsonschema:"Successful tool calls in the window"`
	Denied         int `json:"denied"`
	Errors         int `json:"errors"`
	ActiveUsers    int `json:"active_users" jsonschema:"Distinct users with a successful call in the window"`
	Searches       int `json:"searches" jsonschema:"search_knowledge, get_task_context and start_task calls"`
	DocumentReads  int `json:"document_reads"`
	DocumentWrites int `json:"document_writes" jsonschema:"New document revisions (creates and edits)"`
	DocumentsNew   int `json:"documents_new"`
	TasksStarted   int `json:"tasks_started"`
	TasksCompleted int `json:"tasks_completed"`
	TasksAbandoned int `json:"tasks_abandoned"`
	IssuesOpened   int `json:"issues_opened"`
	SearchMisses   int `json:"search_misses" jsonschema:"search_knowledge calls that returned no hits"`
}

type UsageInventory struct {
	Documents        int          `json:"documents"`
	ByKind           []UsageCount `json:"by_kind"`
	ByStatus         []UsageCount `json:"by_status"`
	OpenTasks        int          `json:"open_tasks"`
	OpenIssues       int          `json:"open_issues"`
	Attachments      int          `json:"attachments"`
	AttachmentBytes  int64        `json:"attachment_bytes"`
	Members          int          `json:"members"`
	EmbeddingsReady  int          `json:"embeddings_ready"`
	EmbeddingsTotal  int          `json:"embeddings_total"`
	EmbeddingsFailed int          `json:"embeddings_failed"`
}

// UsageTodoWeek counts to-dos created and completed in one Monday-start week
// (Asia/Seoul). The first and last weeks may extend past the window; only days
// inside the window are counted.
type UsageTodoWeek struct {
	WeekStart string `json:"week_start" jsonschema:"Monday of the week, YYYY-MM-DD"`
	Created   int    `json:"created"`
	Completed int    `json:"completed"`
}

// UsageTodoBacklog is the waiting to-dos (not completed, not ideas) of one priority.
type UsageTodoBacklog struct {
	Priority       string `json:"priority" jsonschema:"high, medium or low"`
	NotStarted     int    `json:"not_started"`
	InProgress     int    `json:"in_progress"`
	InReview       int    `json:"in_review"`
	PendingRelease int    `json:"pending_release"`
	Total          int    `json:"total"`
	OldestDays     int    `json:"oldest_days" jsonschema:"Whole days since the oldest waiting to-do of this priority was created; 0 when none"`
}

// UsageTodos summarizes the project's to-do board (할 일).
type UsageTodos struct {
	Created        int                `json:"created" jsonschema:"To-dos created in the window"`
	Completed      int                `json:"completed" jsonschema:"To-dos whose completion falls in the window and that are still completed"`
	AvgLeadDays    *float64           `json:"avg_lead_days" jsonschema:"Average days from creation to completion of the to-dos completed in the window; null when none"`
	MedianLeadDays *float64           `json:"median_lead_days"`
	Waiting        int                `json:"waiting" jsonschema:"Current to-dos in not_started, in_progress, in_review or pending_release"`
	Ideas          int                `json:"ideas"`
	Weekly         []UsageTodoWeek    `json:"weekly" jsonschema:"Weeks overlapping the window, oldest first, zero-filled"`
	Backlog        []UsageTodoBacklog `json:"backlog" jsonschema:"Waiting to-dos per priority: high, medium, low (always three rows)"`
}

type UsageStats struct {
	ProjectID     string               `json:"project_id"`
	Days          int                  `json:"days"`
	Timezone      string               `json:"timezone"`
	From          string               `json:"from" jsonschema:"First day of the window, YYYY-MM-DD"`
	To            string               `json:"to" jsonschema:"Last day of the window (today), YYYY-MM-DD"`
	Totals        UsageTotals          `json:"totals"`
	Inventory     UsageInventory       `json:"inventory"`
	Daily         []UsageDay           `json:"daily" jsonschema:"One entry per day of the window, oldest first, zero-filled"`
	TopActions    []UsageCount         `json:"top_actions"`
	TopUsers      []UsageUser          `json:"top_users"`
	TopDocuments  []UsageDocument      `json:"top_documents" jsonschema:"Most read documents (get_document) in the window"`
	SearchMisses  []UsageSearchMiss    `json:"search_misses" jsonschema:"Most frequent zero-result search_knowledge queries (normalized, lowercase), at most 20"`
	SearchLatency []UsageSearchLatency `json:"search_latency" jsonschema:"Search time per retrieval path from search_knowledge and task context searches in the window"`
	HNSWReview    UsageHNSWReview      `json:"hnsw_review" jsonschema:"Vector search times in the window against the project's HNSW review thresholds"`
	KindGrowth    []UsageKindGrowth    `json:"kind_growth"`
	Todos         UsageTodos           `json:"todos"`
	Guidance      string               `json:"guidance"`
}

// usageWindow defines the window as whole Asia/Seoul days ending today.
// Parameters: $1 project ID, $2 days, $3 time zone. Exposes w(start_at, from_day, to_day)
// and a (successful audit events of the project in the window).
const usageWindow = `WITH w AS (
		SELECT ((now() AT TIME ZONE $3)::date-($2::int-1)) AS from_day, (now() AT TIME ZONE $3)::date AS to_day),
	ws AS (SELECT from_day, to_day, (from_day::timestamp AT TIME ZONE $3) AS start_at FROM w),
	a AS (SELECT e.* FROM audit_events e, ws WHERE e.project_id=$1 AND e.created_at>=ws.start_at)`

func usageStats(ctx context.Context, tx *sql.Tx, a Identity, in UsageStatsInput, hnswDefaults HNSWThresholds) (any, error) {
	if _, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false); err != nil {
		return nil, err
	}
	if in.Days == 0 {
		in.Days = 30
	}
	if in.Days < 1 || in.Days > 365 {
		return nil, problem("INVALID_INPUT", "days must be 1–365.")
	}
	args := []any{in.ProjectID, in.Days, usageTimezone, usageSearchActions, usageReadActions}
	out := UsageStats{ProjectID: in.ProjectID, Days: in.Days, Timezone: usageTimezone,
		Guidance: "Calls, searches, reads and active users come from the audit log and only cover what AUDIT_RETENTION_DAYS keeps; document writes come from revisions. Searches include start_task and get_task_context. search_misses lists zero-result search_knowledge queries, kept at most 365 days without the searching user; read them as knowledge to write or glossary synonyms to add. search_latency summarizes measured search times (same retention as search_misses); compare p95_vector_ms with max_scanned_chunks to decide when exact vector search needs an HNSW index. hnsw_review applies the project's thresholds (set_search_thresholds, or the server default) to all vector searches in the window: status review means a threshold is reached. todos covers the to-do board: created and completed in the window, lead time from creation to completion, weekly flow and the waiting backlog per priority (ideas excluded). The call that reads these statistics is not counted."}

	type header struct {
		From   string      `json:"from"`
		To     string      `json:"to"`
		Totals UsageTotals `json:"totals"`
	}
	h, err := one[header](ctx, tx, usageWindow+`,
	r AS (SELECT v.* FROM revisions v JOIN documents d ON d.id=v.document_id, ws WHERE d.project_id=$1 AND v.created_at>=ws.start_at),
	t AS (SELECT tr.* FROM task_runs tr, ws WHERE tr.project_id=$1 AND tr.started_at>=ws.start_at)
	SELECT jsonb_build_object('from',to_char(ws.from_day,'YYYY-MM-DD'),'to',to_char(ws.to_day,'YYYY-MM-DD'),'totals',jsonb_build_object(
		'calls',(SELECT count(*) FROM a WHERE outcome='success'),
		'denied',(SELECT count(*) FROM a WHERE outcome='denied'),
		'errors',(SELECT count(*) FROM a WHERE outcome='error'),
		'active_users',(SELECT count(DISTINCT actor_id) FROM a WHERE outcome='success'),
		'searches',(SELECT count(*) FROM a WHERE outcome='success' AND action=ANY($4::text[])),
		'document_reads',(SELECT count(*) FROM a WHERE outcome='success' AND action=ANY($5::text[])),
		'document_writes',(SELECT count(*) FROM r),
		'documents_new',(SELECT count(*) FROM documents d WHERE d.project_id=$1 AND d.created_at>=ws.start_at),
		'tasks_started',(SELECT count(*) FROM t),
		'tasks_completed',(SELECT count(*) FROM task_runs x WHERE x.project_id=$1 AND x.status='completed' AND x.completed_at>=ws.start_at),
		'tasks_abandoned',(SELECT count(*) FROM t WHERE status='abandoned'),
		'issues_opened',(SELECT count(*) FROM issues i WHERE i.project_id=$1 AND i.created_at>=ws.start_at),
		'search_misses',(SELECT count(*) FROM search_misses m WHERE m.project_id=$1 AND m.created_at>=ws.start_at)))
	FROM ws`, args...)
	if err != nil {
		return nil, err
	}
	out.From, out.To, out.Totals = h.From, h.To, h.Totals

	if out.Inventory, err = one[UsageInventory](ctx, tx, `SELECT jsonb_build_object(
		'documents',(SELECT count(*) FROM documents WHERE project_id=$1),
		'by_kind',(SELECT coalesce(jsonb_agg(jsonb_build_object('key',kind,'count',n) ORDER BY n DESC,kind),'[]') FROM (SELECT kind,count(*) n FROM documents WHERE project_id=$1 GROUP BY kind) k),
		'by_status',(SELECT coalesce(jsonb_agg(jsonb_build_object('key',status,'count',n) ORDER BY n DESC,status),'[]') FROM (SELECT status,count(*) n FROM documents WHERE project_id=$1 GROUP BY status) s),
		'open_tasks',(SELECT count(*) FROM task_runs WHERE project_id=$1 AND status='open'),
		'open_issues',(SELECT count(*) FROM issues WHERE project_id=$1 AND status IN ('open','answered')),
		'attachments',(SELECT count(*) FROM attachments WHERE project_id=$1 AND status='ready'),
		'attachment_bytes',(SELECT coalesce(sum(size_bytes),0) FROM attachments WHERE project_id=$1 AND status='ready'),
		'members',(SELECT count(*) FROM project_members WHERE project_id=$1),
		'embeddings_total',(SELECT count(*) FROM chunks c JOIN documents d ON d.id=c.document_id WHERE d.project_id=$1),
		'embeddings_ready',(SELECT count(*) FROM chunks c JOIN documents d ON d.id=c.document_id WHERE d.project_id=$1 AND c.embedding_status='ready'),
		'embeddings_failed',(SELECT count(*) FROM chunks c JOIN documents d ON d.id=c.document_id WHERE d.project_id=$1 AND c.embedding_status='failed'))`, in.ProjectID); err != nil {
		return nil, err
	}

	if out.Daily, err = many[UsageDay](ctx, tx, usageWindow+`,
	days AS (SELECT generate_series(ws.from_day,ws.to_day,interval '1 day')::date AS day FROM ws),
	ad AS (SELECT (created_at AT TIME ZONE $3)::date AS day,count(*) AS calls,
		count(*) FILTER (WHERE action=ANY($4::text[])) AS searches,count(DISTINCT actor_id) AS users
		FROM a WHERE outcome='success' GROUP BY 1),
	rd AS (SELECT (v.created_at AT TIME ZONE $3)::date AS day,count(*) AS n FROM revisions v JOIN documents d ON d.id=v.document_id, ws
		WHERE d.project_id=$1 AND v.created_at>=ws.start_at GROUP BY 1),
	td AS (SELECT (tr.started_at AT TIME ZONE $3)::date AS day,count(*) AS n FROM task_runs tr, ws
		WHERE tr.project_id=$1 AND tr.started_at>=ws.start_at GROUP BY 1)
	SELECT jsonb_build_object('date',to_char(days.day,'YYYY-MM-DD'),'calls',coalesce(ad.calls,0),'searches',coalesce(ad.searches,0),
		'active_users',coalesce(ad.users,0),'document_writes',coalesce(rd.n,0),'tasks_started',coalesce(td.n,0))
	FROM days LEFT JOIN ad USING(day) LEFT JOIN rd USING(day) LEFT JOIN td USING(day) ORDER BY days.day`, args[:4]...); err != nil {
		return nil, err
	}

	if out.TopActions, err = many[UsageCount](ctx, tx, usageWindow+`
	SELECT jsonb_build_object('key',action,'count',count(*)) FROM a WHERE outcome='success'
	GROUP BY action ORDER BY count(*) DESC,action LIMIT 15`, args[:3]...); err != nil {
		return nil, err
	}

	// Members and anyone who acted in the window (a former member keeps their history).
	if out.TopUsers, err = many[UsageUser](ctx, tx, usageWindow+`,
	ac AS (SELECT actor_id AS id,count(*) AS calls,max(created_at) AS last FROM a WHERE outcome='success' AND actor_id IS NOT NULL GROUP BY actor_id),
	rw AS (SELECT v.actor_id AS id,count(*) AS n FROM revisions v JOIN documents d ON d.id=v.document_id, ws
		WHERE d.project_id=$1 AND v.created_at>=ws.start_at GROUP BY v.actor_id),
	tk AS (SELECT tr.created_by AS id,count(*) AS n FROM task_runs tr, ws WHERE tr.project_id=$1 AND tr.started_at>=ws.start_at GROUP BY tr.created_by),
	ids AS (SELECT id FROM ac UNION SELECT id FROM rw UNION SELECT id FROM tk)
	SELECT jsonb_build_object('user_id',u.id,'name',u.name,'calls',coalesce(ac.calls,0),'document_writes',coalesce(rw.n,0),
		'tasks_started',coalesce(tk.n,0),'last_active_at',ac.last)
	FROM ids JOIN users u ON u.id=ids.id LEFT JOIN ac ON ac.id=ids.id LEFT JOIN rw ON rw.id=ids.id LEFT JOIN tk ON tk.id=ids.id
	ORDER BY coalesce(ac.calls,0)+coalesce(rw.n,0) DESC,u.name,u.id LIMIT 10`, args[:3]...); err != nil {
		return nil, err
	}

	if out.TopDocuments, err = many[UsageDocument](ctx, tx, usageWindow+`,
	g AS (SELECT target_id,count(*) AS n FROM a WHERE outcome='success' AND action=ANY($4::text[]) AND target_id<>'' GROUP BY target_id)
	SELECT jsonb_build_object('document_id',d.id,'title',d.title,'kind',d.kind,'status',d.status,'reads',g.n)
	FROM g JOIN documents d ON d.id::text=g.target_id AND d.project_id=$1 ORDER BY g.n DESC,d.title,d.id LIMIT 10`,
		in.ProjectID, in.Days, usageTimezone, usageReadActions); err != nil {
		return nil, err
	}
	if out.SearchMisses, err = many[UsageSearchMiss](ctx, tx, usageWindow+`
	SELECT jsonb_build_object('query',m.query,'count',count(*),'last_at',max(m.created_at)) FROM search_misses m, ws
	WHERE m.project_id=$1 AND m.created_at>=ws.start_at GROUP BY m.query ORDER BY count(*) DESC,max(m.created_at) DESC,m.query LIMIT 20`, args[:3]...); err != nil {
		return nil, err
	}

	if out.SearchLatency, err = many[UsageSearchLatency](ctx, tx, usageWindow+`,
	t AS (SELECT st.* FROM search_timings st, ws WHERE st.project_id=$1 AND st.created_at>=ws.start_at)
	SELECT jsonb_build_object('retrieval',retrieval,'searches',count(*),
		'p50_ms',round(percentile_cont(0.5) WITHIN GROUP (ORDER BY total_ms::float8)::numeric,2),
		'p95_ms',round(percentile_cont(0.95) WITHIN GROUP (ORDER BY total_ms::float8)::numeric,2),
		'max_ms',round(max(total_ms)::numeric,2),
		'avg_embed_ms',round(avg(embed_ms)::numeric,2),
		'p95_vector_ms',round((percentile_cont(0.95) WITHIN GROUP (ORDER BY vector_ms::float8))::numeric,2),
		'avg_lexical_ms',round(avg(lexical_ms)::numeric,2),
		'max_scanned_chunks',max(scanned_chunks),'last_at',max(created_at))
	FROM t GROUP BY retrieval ORDER BY count(*) DESC,retrieval`, args[:3]...); err != nil {
		return nil, err
	}
	thresholds, err := projectSearchThresholds(ctx, tx, in.ProjectID, hnswDefaults)
	if err != nil {
		return nil, err
	}
	type vectorSummary struct {
		Searches  int      `json:"searches"`
		P95MS     *float64 `json:"p95_ms"`
		MaxChunks int      `json:"max_chunks"`
	}
	vs, err := one[vectorSummary](ctx, tx, usageWindow+`
	SELECT jsonb_build_object('searches',count(*),
		'p95_ms',round((percentile_cont(0.95) WITHIN GROUP (ORDER BY vector_ms::float8))::numeric,2),
		'max_chunks',coalesce(max(scanned_chunks),0))
	FROM search_timings st, ws WHERE st.project_id=$1 AND st.created_at>=ws.start_at AND st.vector_ms IS NOT NULL`, args[:3]...)
	if err != nil {
		return nil, err
	}
	out.HNSWReview = hnswReview(thresholds, vs.Searches, vs.P95MS, vs.MaxChunks)

	if out.KindGrowth, err = many[UsageKindGrowth](ctx, tx, usageWindow+`,
	days AS (SELECT generate_series(ws.from_day,ws.to_day,interval '1 day')::date AS day FROM ws),
	k AS (SELECT DISTINCT kind FROM documents WHERE project_id=$1),
	starts AS (SELECT d.kind,count(*) AS n FROM documents d, ws WHERE d.project_id=$1 AND d.created_at<ws.start_at GROUP BY d.kind),
	adds AS (SELECT d.kind,(d.created_at AT TIME ZONE $3)::date AS day,count(*) AS n FROM documents d, ws
		WHERE d.project_id=$1 AND d.created_at>=ws.start_at GROUP BY 1,2),
	per AS (SELECT k.kind,days.day,coalesce(adds.n,0) AS added,coalesce(starts.n,0) AS start FROM k CROSS JOIN days
		LEFT JOIN adds ON adds.kind=k.kind AND adds.day=days.day LEFT JOIN starts ON starts.kind=k.kind),
	run AS (SELECT per.*,sum(added) OVER (PARTITION BY kind ORDER BY day) AS running FROM per)
	SELECT jsonb_build_object('kind',kind,'start',start,'added',sum(added),'total',start+sum(added),'cumulative',jsonb_agg(start+running ORDER BY day))
	FROM run GROUP BY kind,start ORDER BY start+sum(added) DESC,kind`, args[:3]...); err != nil {
		return nil, err
	}
	if out.Todos, err = usageTodos(ctx, tx, args[:3]); err != nil {
		return nil, err
	}
	return out, nil
}

// todoWaitingStatuses are the board columns that still need work.
const todoWaitingStatuses = `('not_started','in_progress','in_review','pending_release')`

func usageTodos(ctx context.Context, tx *sql.Tx, args []any) (UsageTodos, error) {
	out, err := one[UsageTodos](ctx, tx, usageWindow+`,
	d AS (SELECT extract(epoch FROM t.completed_at-t.created_at)/86400.0 AS lead FROM todos t, ws
		WHERE t.project_id=$1 AND t.status='completed' AND t.completed_at>=ws.start_at)
	SELECT jsonb_build_object(
		'created',(SELECT count(*) FROM todos t WHERE t.project_id=$1 AND t.created_at>=ws.start_at),
		'completed',(SELECT count(*) FROM d),
		'avg_lead_days',(SELECT round(avg(lead)::numeric,1) FROM d),
		'median_lead_days',(SELECT round((percentile_cont(0.5) WITHIN GROUP (ORDER BY lead))::numeric,1) FROM d),
		'waiting',(SELECT count(*) FROM todos t WHERE t.project_id=$1 AND t.status IN `+todoWaitingStatuses+`),
		'ideas',(SELECT count(*) FROM todos t WHERE t.project_id=$1 AND t.status='idea'))
	FROM ws`, args...)
	if err != nil {
		return out, err
	}
	if out.Weekly, err = many[UsageTodoWeek](ctx, tx, usageWindow+`,
	wk AS (SELECT generate_series(date_trunc('week',ws.from_day),date_trunc('week',ws.to_day),interval '7 days')::date AS week FROM ws),
	c AS (SELECT date_trunc('week',t.completed_at AT TIME ZONE $3)::date AS week,count(*) AS n FROM todos t, ws
		WHERE t.project_id=$1 AND t.status='completed' AND t.completed_at>=ws.start_at GROUP BY 1),
	n AS (SELECT date_trunc('week',t.created_at AT TIME ZONE $3)::date AS week,count(*) AS n FROM todos t, ws
		WHERE t.project_id=$1 AND t.created_at>=ws.start_at GROUP BY 1)
	SELECT jsonb_build_object('week_start',to_char(wk.week,'YYYY-MM-DD'),'created',coalesce(n.n,0),'completed',coalesce(c.n,0))
	FROM wk LEFT JOIN c USING(week) LEFT JOIN n USING(week) ORDER BY wk.week`, args...); err != nil {
		return out, err
	}
	out.Backlog, err = many[UsageTodoBacklog](ctx, tx, `SELECT jsonb_build_object('priority',p.priority,
		'not_started',count(t.id) FILTER (WHERE t.status='not_started'),
		'in_progress',count(t.id) FILTER (WHERE t.status='in_progress'),
		'in_review',count(t.id) FILTER (WHERE t.status='in_review'),
		'pending_release',count(t.id) FILTER (WHERE t.status='pending_release'),
		'total',count(t.id),
		'oldest_days',coalesce(floor(extract(epoch FROM now()-min(t.created_at))/86400),0)::int)
	FROM (VALUES ('high',0),('medium',1),('low',2)) AS p(priority,ord)
	LEFT JOIN todos t ON t.project_id=$1 AND t.priority=p.priority AND t.status IN `+todoWaitingStatuses+`
	GROUP BY p.priority,p.ord ORDER BY p.ord`, args[0])
	return out, err
}
