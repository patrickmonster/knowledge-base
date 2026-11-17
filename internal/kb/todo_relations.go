package kb

import (
	"context"
	"database/sql"
	"strings"
)

// To-do relations (migrations/043_todo_relations.sql):
//   - parent_id makes a to-do a sub-item of another. Only one level: a parent
//     is never a sub-item itself, and a to-do with sub-items gets no parent.
//   - todo_dependencies lists the to-dos one waits for (blocked_by). The graph
//     stays acyclic.
//
// Both stay inside one project. They are shown on the board only; moving or
// completing a to-do is never refused because of them.

const maxTodoBlockers = 20

// TodoRelated is a to-do referred to from another one.
type TodoRelated struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// todoRelationsSelect adds the relation fields to todoSelect (alias t).
const todoRelationsSelect = `jsonb_build_object(
	'parent_title',COALESCE((SELECT pt.title FROM todos pt WHERE pt.id=t.parent_id),''),
	'blocked_by',COALESCE((SELECT jsonb_agg(jsonb_build_object('id',b.id,'title',b.title,'status',b.status) ORDER BY b.created_at,b.id)
		FROM todo_dependencies d JOIN todos b ON b.id=d.blocked_by WHERE d.todo_id=t.id),'[]'::jsonb),
	'open_blocker_count',(SELECT count(*) FROM todo_dependencies d JOIN todos b ON b.id=d.blocked_by WHERE d.todo_id=t.id AND b.status<>'completed'),
	'subtask_count',(SELECT count(*) FROM todos st WHERE st.parent_id=t.id),
	'subtasks_completed',(SELECT count(*) FROM todos st WHERE st.parent_id=t.id AND st.status='completed'))`

// normalizeTodoRelations checks the relation fields' shape before any lookup:
// canonical IDs, no duplicates, at most maxTodoBlockers predecessors.
func normalizeTodoRelations(in *SaveTodoInput) error {
	if in.ParentID != nil {
		parent := strings.TrimSpace(*in.ParentID)
		if parent != "" {
			if err := validID(parent); err != nil {
				return problem("INVALID_INPUT", "parent_id must be a to-do ID.")
			}
			if parent == in.TodoID {
				return problem("INVALID_INPUT", "A to-do cannot be its own parent.")
			}
		}
		in.ParentID = &parent
	}
	if in.BlockedBy != nil {
		seen := map[string]bool{}
		ids := []string{}
		for _, raw := range *in.BlockedBy {
			id := strings.TrimSpace(raw)
			if err := validID(id); err != nil {
				return problem("INVALID_INPUT", "blocked_by must list to-do IDs.")
			}
			if id == in.TodoID {
				return problem("INVALID_INPUT", "A to-do cannot wait for itself.")
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if len(ids) > maxTodoBlockers {
			return problem("INVALID_INPUT", "blocked_by can list at most 20 to-dos.")
		}
		in.BlockedBy = &ids
	}
	return nil
}

// setTodoRelations replaces the given relations of a saved to-do; nil leaves
// that relation as it is.
func setTodoRelations(ctx context.Context, tx *sql.Tx, projectID, todoID string, parentID *string, blockedBy *[]string) error {
	if parentID == nil && blockedBy == nil {
		return nil
	}
	// Serialize relation changes per project so two saves cannot each pass the
	// checks and together form a cycle or a second level. A separate key from
	// move_todo's board lock keeps the two from waiting on each other.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('todo-relations:'||$1,0))`, projectID); err != nil {
		return err
	}
	if parentID != nil {
		if err := setTodoParent(ctx, tx, projectID, todoID, *parentID); err != nil {
			return err
		}
	}
	if blockedBy != nil {
		if err := setTodoBlockers(ctx, tx, projectID, todoID, *blockedBy); err != nil {
			return err
		}
	}
	return nil
}

func setTodoParent(ctx context.Context, tx *sql.Tx, projectID, todoID, parentID string) error {
	if parentID != "" {
		var grandparent sql.NullString
		err := tx.QueryRowContext(ctx, `SELECT parent_id FROM todos WHERE id=$1 AND project_id=$2`, parentID, projectID).Scan(&grandparent)
		if err == sql.ErrNoRows {
			return problem("INVALID_INPUT", "parent_id must be a to-do in this project.")
		}
		if err != nil {
			return err
		}
		if grandparent.Valid {
			return problem("INVALID_INPUT", "The parent is itself a sub-item; sub-items are one level deep.")
		}
		var children bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM todos WHERE parent_id=$1)`, todoID).Scan(&children); err != nil {
			return err
		}
		if children {
			return problem("INVALID_INPUT", "A to-do with sub-items cannot become a sub-item.")
		}
	}
	_, err := tx.ExecContext(ctx, `UPDATE todos SET parent_id=NULLIF($2,'')::uuid WHERE id=$1`, todoID, parentID)
	return err
}

func setTodoBlockers(ctx context.Context, tx *sql.Tx, projectID, todoID string, ids []string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM todo_dependencies WHERE todo_id=$1`, todoID); err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	var found int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM todos WHERE project_id=$1 AND id=ANY($2::uuid[])`, projectID, ids).Scan(&found); err != nil {
		return err
	}
	if found != len(ids) {
		return problem("INVALID_INPUT", "blocked_by must list to-dos in this project.")
	}
	// Walk what the new predecessors wait for, transitively; reaching this
	// to-do means it would end up waiting for itself.
	var cycle bool
	if err := tx.QueryRowContext(ctx, `WITH RECURSIVE up(id) AS (
			SELECT unnest($1::uuid[])
			UNION SELECT d.blocked_by FROM todo_dependencies d JOIN up ON d.todo_id=up.id)
		SELECT EXISTS(SELECT 1 FROM up WHERE id=$2)`, ids, todoID).Scan(&cycle); err != nil {
		return err
	}
	if cycle {
		return problem("INVALID_INPUT", "blocked_by would form a cycle: one of those to-dos already waits for this one.")
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO todo_dependencies(todo_id,blocked_by) SELECT $1,unnest($2::uuid[])`, todoID, ids)
	return err
}
