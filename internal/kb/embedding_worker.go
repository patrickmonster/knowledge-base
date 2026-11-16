package kb

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

type embeddingJob struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// embeddingQueue is a chunk table the worker fills with vectors. Both tables
// share the queue columns from migrations 003 and 041.
type embeddingQueue struct {
	table string
	// text selects {id,text} model inputs from the claimed rows c (id, owner, content).
	text string
}

var (
	documentEmbeddingQueue = embeddingQueue{table: "chunks", text: `SELECT jsonb_build_object('id',c.id,'text',d.title||E'\n'||array_to_string(d.tags,' ')||E'\n\n'||c.content)
		FROM claimed c JOIN documents d ON d.id=c.document_id`}
	// Issue chunks name their kind so a question and a document with the same
	// words still read differently to the model.
	issueEmbeddingQueue = embeddingQueue{table: "issue_chunks", text: `SELECT jsonb_build_object('id',c.id,'text',
		CASE i.kind WHEN 'issue' THEN '이슈: ' ELSE '질의: ' END||i.title||E'\n'||array_to_string(i.tags,' ')||E'\n\n'||c.content)
		FROM claimed c JOIN issues i ON i.id=c.issue_id`}
)

// New/replaced chunks are atomically queued by indexDocument via column defaults.
// Leases prevent duplicate work across replicas. Model calls never hold a DB transaction.
func (s *Store) ProcessEmbeddingBatch(ctx context.Context) (int, error) {
	return s.processEmbeddingQueue(ctx, documentEmbeddingQueue)
}

// ProcessIssueEmbeddingBatch chunks questions that have none yet (those saved
// before issue chunks existed), then embeds up to 8 pending issue chunks.
func (s *Store) ProcessIssueEmbeddingBatch(ctx context.Context) (int, error) {
	if s.Embedder == nil {
		return 0, problem("EMBEDDING_DISABLED", "No local embedder is configured.")
	}
	chunked, err := s.chunkUnindexedIssues(ctx)
	if err != nil {
		return 0, err
	}
	n, err := s.processEmbeddingQueue(ctx, issueEmbeddingQueue)
	return n + chunked, err
}

func (s *Store) processEmbeddingQueue(ctx context.Context, q embeddingQueue) (int, error) {
	if s.Embedder == nil {
		return 0, problem("EMBEDDING_DISABLED", "No local embedder is configured.")
	}
	lease := uuid.NewString()
	model := s.Embedder.Model()
	var jobs []embeddingJob
	err := transaction(ctx, s.DB, func(tx *sql.Tx) error {
		// A worker can die on its last attempt; terminal leases must not stay processing forever.
		if _, err := tx.ExecContext(ctx, `UPDATE `+q.table+` SET embedding_status='failed',embedding_lease=NULL,
		embedding_lease_until=NULL,embedding_error='EMBEDDING_LEASE_EXPIRED'
		WHERE embedding_status='processing' AND embedding_lease_until<now() AND embedding_attempts>=5`); err != nil {
			return err
		}
		var err error
		jobs, err = many[embeddingJob](ctx, tx, `WITH candidates AS (
		SELECT id FROM `+q.table+` WHERE
		  ((embedding_status IN ('pending','failed') AND embedding_attempts<5 AND embedding_retry_at<=now())
		   OR (embedding_status='processing' AND embedding_lease_until<now() AND embedding_attempts<5)
		   OR (embedding_status='ready' AND embedding_model IS DISTINCT FROM $1))
		ORDER BY embedding_retry_at,id LIMIT 8 FOR UPDATE SKIP LOCKED
		), claimed AS (
		UPDATE `+q.table+` c SET embedding_status='processing',embedding=NULL,embedding_model=$1,
		 embedding_attempts=CASE WHEN c.embedding_model IS DISTINCT FROM $1 THEN 1 ELSE c.embedding_attempts+1 END,
		 embedding_lease=$2,embedding_lease_until=now()+interval '150 seconds',embedding_error=NULL
		FROM candidates WHERE c.id=candidates.id RETURNING c.*
		) `+q.text, model, lease)
		return err
	})
	if err != nil || len(jobs) == 0 {
		return 0, err
	}
	texts := make([]string, len(jobs))
	for i, job := range jobs {
		texts[i] = job.Text
	}
	embedCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	vectors, embedErr := s.Embedder.Embed(embedCtx, texts)
	cancel()
	if embedErr == nil {
		embedErr = validateVectors(vectors, len(jobs))
	}
	// Complete even if shutdown canceled the model call, so retries need not await lease expiry.
	saveCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer stop()
	err = transaction(saveCtx, s.DB, func(tx *sql.Tx) error {
		for i, job := range jobs {
			if embedErr != nil {
				_, err := tx.ExecContext(saveCtx, `UPDATE `+q.table+` SET embedding_status='failed',embedding_lease=NULL,embedding_lease_until=NULL,
				embedding_error=$3,embedding_retry_at=now()+make_interval(secs=>LEAST(3600,30*power(2,embedding_attempts-1))::integer)
				WHERE id=$1 AND embedding_lease=$2`, job.ID, lease, errorCode(embedErr))
				if err != nil {
					return err
				}
			} else {
				// A document edit replaces chunk IDs. A reindex clears the lease.
				// Either change makes stale results harmless no-ops.
				_, err := tx.ExecContext(saveCtx, `UPDATE `+q.table+` SET embedding=$3::public.vector,embedding_status='ready',
				embedding_lease=NULL,embedding_lease_until=NULL,embedding_error=NULL WHERE id=$1 AND embedding_lease=$2 AND embedding_model=$4`,
					job.ID, lease, vectorLiteral(vectors[i]), model)
				if err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return len(jobs), err
	}
	return len(jobs), embedErr
}

func (s *Store) RunEmbeddingWorker(ctx context.Context) {
	for ctx.Err() == nil {
		count, err := s.ProcessEmbeddingBatch(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Warn("embedding batch failed; inspect get_embedding_status", "code", errorCode(err))
		}
		issues, issueErr := s.ProcessIssueEmbeddingBatch(ctx)
		if issueErr != nil && ctx.Err() == nil {
			slog.Warn("issue embedding batch failed; inspect get_embedding_status", "code", errorCode(issueErr))
		}
		if (count > 0 && err == nil) || (issues > 0 && issueErr == nil) {
			continue
		}
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (s *Store) embeddingStatus(ctx context.Context, tx *sql.Tx, actor Identity, projectID string) (any, error) {
	if _, err := projectAccess(ctx, tx, actor.User.ID, projectID, false); err != nil {
		return nil, err
	}
	model := ""
	if s.Embedder != nil {
		model = s.Embedder.Model()
	}
	out, err := one[map[string]any](ctx, tx, `SELECT jsonb_build_object('configured',$2<>'','model',$2,'dimensions',1024,
	'total',count(*),'ready',count(*) FILTER(WHERE embedding_status='ready' AND embedding_model=$2),
	'pending',count(*) FILTER(WHERE embedding_status='pending' OR (embedding_status='ready' AND embedding_model IS DISTINCT FROM $2)),
	'processing',count(*) FILTER(WHERE embedding_status='processing'),'failed',count(*) FILTER(WHERE embedding_status='failed'),
	'retries_exhausted',count(*) FILTER(WHERE embedding_attempts>=5 AND embedding_status<>'ready'),
	'error_codes',COALESCE(jsonb_agg(DISTINCT embedding_error) FILTER(WHERE embedding_error IS NOT NULL),'[]'::jsonb))
	FROM chunks c JOIN documents d ON d.id=c.document_id WHERE d.project_id=$1`, projectID, model)
	if err != nil {
		return nil, err
	}
	// Question and issue chunks are counted apart so document totals keep their
	// meaning; unchunked counts questions the worker has not split yet.
	out["issues"], err = one[map[string]any](ctx, tx, `SELECT jsonb_build_object(
	'total',count(c.id),'ready',count(c.id) FILTER(WHERE c.embedding_status='ready' AND c.embedding_model=$2),
	'pending',count(c.id) FILTER(WHERE c.embedding_status='pending' OR (c.embedding_status='ready' AND c.embedding_model IS DISTINCT FROM $2)),
	'processing',count(c.id) FILTER(WHERE c.embedding_status='processing'),'failed',count(c.id) FILTER(WHERE c.embedding_status='failed'),
	'retries_exhausted',count(c.id) FILTER(WHERE c.embedding_attempts>=5 AND c.embedding_status<>'ready'),
	'unchunked',count(*) FILTER(WHERE c.id IS NULL),
	'error_codes',COALESCE(jsonb_agg(DISTINCT c.embedding_error) FILTER(WHERE c.embedding_error IS NOT NULL),'[]'::jsonb))
	FROM issues i LEFT JOIN issue_chunks c ON c.issue_id=i.id WHERE i.project_id=$1`, projectID, model)
	return out, err
}
