package kb

import (
	"context"
	"database/sql"
)

// viewableTaskContext trims a stored start_task snapshot to what the reader
// may see now. The snapshot was taken with its starter's access, but any
// member of the task's project can read the run, so:
//   - related sources stay only for projects the reader is a member of
//     (a key scoped to one project reaches no other project);
//   - upstream sources stay only for upstreams the project still follows,
//     since the approved link is what lets its members read them.
//
// Project names come from the current projects so a renamed project shows its
// current name. The number of dropped documents is reported in
// related_hidden / upstream_hidden so a reader knows the list is partial.
func viewableTaskContext(ctx context.Context, tx *sql.Tx, a Identity, projectID string, c *TaskContext) error {
	if len(c.RelatedProjects) > 0 || len(c.RelatedSources) > 0 {
		readable := []SearchedProject{}
		if a.ProjectID == "" {
			ids := make([]string, 0, len(c.RelatedProjects)+len(c.RelatedSources))
			for _, p := range c.RelatedProjects {
				ids = append(ids, p.ID)
			}
			for _, h := range c.RelatedSources {
				ids = append(ids, h.ProjectID)
			}
			scope, err := projectScope(ids)
			if err != nil {
				return err
			}
			readable, err = many[SearchedProject](ctx, tx, `SELECT jsonb_build_object('id',p.id,'slug',p.slug,'name',p.name,'role',m.role)
			FROM projects p JOIN project_members m ON m.project_id=p.id
			WHERE m.user_id=$1 AND p.id=ANY($2::text::uuid[]) ORDER BY p.created_at,p.id`, a.User.ID, scope)
			if err != nil {
				return err
			}
		}
		c.RelatedProjects, c.RelatedSources, c.RelatedHidden = keepProjects(c.RelatedProjects, c.RelatedSources, readable)
		if len(c.RelatedProjects) == 0 {
			c.RelatedWarning = ""
		}
	}
	if len(c.UpstreamProjects) > 0 || len(c.UpstreamSources) > 0 {
		followed, err := approvedUpstreams(ctx, tx, projectID)
		if err != nil {
			return err
		}
		c.UpstreamProjects, c.UpstreamSources, c.UpstreamHidden = keepProjects(c.UpstreamProjects, c.UpstreamSources, followed)
		if len(c.UpstreamProjects) == 0 {
			c.UpstreamWarning = ""
		}
	}
	return nil
}

// keepProjects keeps the stored projects and hits whose project is in allowed,
// relabels them with allowed's current slug and name, and counts dropped hits.
func keepProjects(projects []SearchedProject, hits []SearchHit, allowed []SearchedProject) ([]SearchedProject, []SearchHit, int) {
	byID := map[string]SearchedProject{}
	for _, p := range allowed {
		byID[p.ID] = p
	}
	var keptProjects []SearchedProject
	for _, p := range projects {
		if now, ok := byID[p.ID]; ok {
			p.Slug, p.Name, p.Role = now.Slug, now.Name, now.Role
			keptProjects = append(keptProjects, p)
		}
	}
	var keptHits []SearchHit
	for _, h := range hits {
		if now, ok := byID[h.ProjectID]; ok {
			h.ProjectSlug, h.ProjectName = now.Slug, now.Name
			keptHits = append(keptHits, h)
		}
	}
	return keptProjects, keptHits, len(hits) - len(keptHits)
}
