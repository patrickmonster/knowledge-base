package kb

import (
	"context"
	"database/sql"
	"strings"
)

// AGENTS.md rollout: other repositories adopt the same task rules (knowledge
// lookup at start, history at finish) with their own project slug.

type AgentsTemplate struct {
	Filename    string   `json:"filename"`
	ProjectID   string   `json:"project_id"`
	ProjectSlug string   `json:"project_slug"`
	Content     string   `json:"content"`
	Notes       []string `json:"notes"`
}

func renderAgentsTemplate(p Project, serverURL string) (AgentsTemplate, error) {
	raw, err := templateFiles.ReadFile("templates/agents.md")
	if err != nil {
		return AgentsTemplate{}, err
	}
	if serverURL == "" {
		serverURL = "(서버 주소)"
	}
	// Names cannot break out of Markdown code spans or the HTML comment.
	name := strings.NewReplacer("`", "'", "-->", "->", "\n", " ", "\r", " ").Replace(p.Name)
	content := strings.NewReplacer("{{PROJECT_SLUG}}", p.Slug, "{{PROJECT_NAME}}", name, "{{SERVER_URL}}", serverURL).Replace(string(raw))
	return AgentsTemplate{Filename: "AGENTS.md", ProjectID: p.ID, ProjectSlug: p.Slug, Content: content, Notes: []string{
		"저장소 루트에 AGENTS.md로 저장하고 커밋합니다. Codex·Cursor 등은 AGENTS.md를 읽습니다.",
		"Claude Code는 CLAUDE.md를 읽습니다. CLAUDE.md 한 줄에 @AGENTS.md 를 적어 같은 규칙을 불러옵니다.",
		"AI 도구에 이 지식베이스 MCP 서버와 해당 프로젝트에 접근 가능한 키를 연결해야 규칙이 동작합니다.",
	}}, nil
}

func registerAgentsTools(t Tools, s *Store) {
	add(t, s, "get_agents_template", "Return an AGENTS.md for another repository that applies this server's task rules (knowledge lookup at start, history at finish) to the given project. Save it at the repository root.", false, func(in ProjectInput) string { return in.ProjectID }, func(ctx context.Context, tx *sql.Tx, a Identity, in ProjectInput) (any, error) {
		p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, false)
		if err != nil {
			return nil, err
		}
		return renderAgentsTemplate(p, s.PublicURL)
	})
}
