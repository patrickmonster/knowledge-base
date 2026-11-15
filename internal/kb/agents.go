package kb

import (
	"context"
	"database/sql"
)

// User-level instructions: the same task rules (knowledge lookup at start,
// history at finish) as a Markdown file for clients that want them in their
// own instruction file. The server instructions already carry the workflow, so
// the file is optional. It names no server address, project name or slug: it
// belongs in user-level settings, never committed to a repository, where such
// names would mark the repository as using this server.

type AgentsTemplate struct {
	Filename    string   `json:"filename"`
	ProjectID   string   `json:"project_id"`
	ProjectSlug string   `json:"project_slug"`
	Content     string   `json:"content"`
	Notes       []string `json:"notes"`
}

func renderAgentsTemplate(p Project) (AgentsTemplate, error) {
	raw, err := templateFiles.ReadFile("templates/agents.md")
	if err != nil {
		return AgentsTemplate{}, err
	}
	return AgentsTemplate{Filename: "AGENTS.md", ProjectID: p.ID, ProjectSlug: p.Slug, Content: string(raw), Notes: []string{
		"선택 사항입니다. 서버가 접속할 때 같은 절차를 지침으로 내려주므로 이 파일이 없어도 동작합니다.",
		"저장소에 커밋하지 마세요. 서버·프로젝트를 쓴다는 흔적이 남습니다. 필요하면 사용자 수준 지침(~/.claude/CLAUDE.md, ~/.codex/AGENTS.md 등)에 넣습니다.",
		"저장소를 이 프로젝트에 연결하려면 소유자·관리자가 그 저장소의 git remote를 등록합니다(add_project_repository). 등록되지 않은 저장소에서는 이 서버를 쓰지 않습니다.",
		"MCP 서버 연결도 커밋되는 프로젝트 설정(.mcp.json 등)이 아니라 사용자 수준 설정에 둡니다.",
	}}, nil
}

func registerAgentsTools(t Tools, s *Store) {
	add(t, s, "get_agents_template", "Return optional user-level instructions (Markdown) with this server's task rules. The server instructions already carry them. The text names no server or project; keep it in user-level settings and never commit it to a repository.", false, func(in ProjectInput) string { return in.ProjectID }, func(ctx context.Context, tx *sql.Tx, a Identity, in ProjectInput) (any, error) {
		p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false)
		if err != nil {
			return nil, err
		}
		return renderAgentsTemplate(p)
	})
}
