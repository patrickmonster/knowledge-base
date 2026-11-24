package kb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Deployment checks (PR 병합·배포 확인).
//
// A history saved by finish_task describes the work when the pull request was
// opened; merging and deploying happen later and used to leave the history
// saying "미병합·미배포". finish_task can now name the pull requests that carry
// the work. The server checks them on GitHub — merged or closed, and for pull
// requests in the server's own repository whether the running build contains
// the merge commit — and, once a pull request reaches a final state, prepends
// a "상태 갱신" block below the history title. The body written at completion
// time is never rewritten. Unsettled pull requests appear in the
// start_task / get_task_context context of the user who finished the task.

const (
	maxTaskPullRequests  = 10
	deploymentCheckBatch = 20
	deploymentRecheckAge = 20 * time.Minute
	deploymentWatchDays  = 30
)

// DeploymentTarget identifies the running server build: the GitHub repository
// it was built from and the commit. Pull requests in that repository count as
// deployed once the commit contains their merge commit. An empty Repository or
// Commit disables the deployed state; merges are still tracked.
type DeploymentTarget struct {
	Repository string `json:"repository,omitempty"`
	Commit     string `json:"commit,omitempty"`
}

// DetectDeploymentTarget reads SERVER_REPOSITORY (owner/name), falling back
// to Railway's RAILWAY_GIT_REPO_OWNER and RAILWAY_GIT_REPO_NAME, and the build
// commit. Only an explicitly set but invalid SERVER_REPOSITORY is an error.
func DetectDeploymentTarget(serverRepository, railwayOwner, railwayName string) (DeploymentTarget, error) {
	out := DeploymentTarget{}
	if repo := strings.TrimSpace(serverRepository); repo != "" {
		parsed, err := parseGitHubRepository(repo)
		if err != nil {
			return out, errors.New("SERVER_REPOSITORY must be owner/name")
		}
		out.Repository = strings.ToLower(parsed)
	} else if railwayOwner != "" && railwayName != "" {
		if parsed, err := parseGitHubRepository(railwayOwner + "/" + railwayName); err == nil {
			out.Repository = strings.ToLower(parsed)
		}
	}
	// A locally modified build does not correspond to any commit on GitHub.
	if commit, source := buildCommit(); source == "build" || source == "railway" || source == "vcs" {
		out.Commit = commit
	}
	return out, nil
}

type pullRef struct {
	Repository string
	Number     int
	URL        string
}

var (
	pullURLPattern = regexp.MustCompile(`^https://github\.com/([^/\s?#]+/[^/\s?#]+)/pull/([0-9]{1,9})(?:/[A-Za-z]*)?/?(?:[?#].*)?$`)
	pullRefPattern = regexp.MustCompile(`^([^/\s#]+/[^/\s#]+)#([0-9]{1,9})$`)
)

// parsePullRequest accepts https://github.com/owner/repo/pull/N (optionally
// with /files, /commits, a query or a fragment) or owner/repo#N.
func parsePullRequest(raw string) (pullRef, error) {
	s := strings.TrimSpace(raw)
	m := pullURLPattern.FindStringSubmatch(s)
	if m == nil {
		m = pullRefPattern.FindStringSubmatch(s)
	}
	if m == nil {
		return pullRef{}, problem("INVALID_INPUT", "pull_requests items must be https://github.com/owner/repo/pull/N or owner/repo#N.")
	}
	repo, err := parseGitHubRepository(m[1])
	if err != nil {
		return pullRef{}, problem("INVALID_INPUT", "pull_requests items must be https://github.com/owner/repo/pull/N or owner/repo#N.")
	}
	n, err := strconv.Atoi(m[2])
	if err != nil || n <= 0 {
		return pullRef{}, problem("INVALID_INPUT", "Pull request numbers must be positive.")
	}
	repo = strings.ToLower(repo)
	return pullRef{Repository: repo, Number: n, URL: fmt.Sprintf("https://github.com/%s/pull/%d", repo, n)}, nil
}

func parsePullRequests(raw []string) ([]pullRef, error) {
	if len(raw) > maxTaskPullRequests {
		return nil, problem("INVALID_INPUT", fmt.Sprintf("At most %d pull requests are allowed.", maxTaskPullRequests))
	}
	out, seen := []pullRef{}, map[string]bool{}
	for _, item := range raw {
		ref, err := parsePullRequest(item)
		if err != nil {
			return nil, err
		}
		if !seen[ref.URL] {
			seen[ref.URL] = true
			out = append(out, ref)
		}
	}
	return out, nil
}

type githubPull struct {
	State          string  `json:"state"`
	Merged         bool    `json:"merged"`
	MergeCommitSHA *string `json:"merge_commit_sha"`
	MergedAt       *string `json:"merged_at"`
}

// PullRequest reads one pull request.
func (c *GitHubClient) PullRequest(ctx context.Context, repo string, number int) (githubPull, error) {
	var out githubPull
	err := c.getJSON(ctx, fmt.Sprintf("%s/repos/%s/pulls/%d", c.baseURL(), repo, number), &out)
	return out, err
}

// Contains reports whether commit is in the history of head. Comparing
// head...commit gives "behind" (or "identical") with an empty diff when commit
// is an ancestor of head, so the response stays small in the expected case.
func (c *GitHubClient) Contains(ctx context.Context, repo, head, commit string) (bool, error) {
	var out struct {
		Status string `json:"status"`
	}
	if err := c.getJSON(ctx, fmt.Sprintf("%s/repos/%s/compare/%s...%s?per_page=1", c.baseURL(), repo, url.PathEscape(head), url.PathEscape(commit)), &out); err != nil {
		return false, err
	}
	return out.Status == "behind" || out.Status == "identical", nil
}

// TrackedPullRequest is one row of task_pull_requests.
type TrackedPullRequest struct {
	TaskID            string  `json:"task_id"`
	ProjectID         string  `json:"project_id"`
	TaskTitle         string  `json:"task_title,omitempty"`
	HistoryDocumentID *string `json:"history_document_id,omitempty"`
	Repository        string  `json:"repository"`
	Number            int     `json:"number"`
	URL               string  `json:"url"`
	State             string  `json:"state"`
	MergeCommit       *string `json:"merge_commit,omitempty"`
	MergedAt          *string `json:"merged_at,omitempty"`
	DeployedCommit    *string `json:"deployed_commit,omitempty"`
	DeployedAt        *string `json:"deployed_at,omitempty"`
	CheckedAt         *string `json:"checked_at,omitempty"`
	CheckError        *string `json:"check_error,omitempty"`
	RecordedState     *string `json:"recorded_state,omitempty"`
	RecordedAt        *string `json:"recorded_at,omitempty"`
	ConfirmedBy       *string `json:"confirmed_by,omitempty"`
	ConfirmedByName   *string `json:"confirmed_by_name,omitempty"`
	ConfirmedAt       *string `json:"confirmed_at,omitempty"`
	CreatedAt         string  `json:"created_at"`
}

// tracksDeployment reports whether this server can tell that a pull request
// in repo is deployed.
func (s *Store) tracksDeployment(repo string) bool {
	return s.Deployment.Repository != "" && s.Deployment.Commit != "" && repo == s.Deployment.Repository
}

// finalState: nothing further will change for this pull request on this
// server, so its state can be written into the history.
func (s *Store) finalState(p TrackedPullRequest) bool {
	switch p.State {
	case "deployed", "closed":
		return true
	case "merged":
		return !s.tracksDeployment(p.Repository)
	}
	return false
}

func insertPullRequests(ctx context.Context, tx *sql.Tx, projectID, taskID, historyID string, refs []pullRef) error {
	for _, ref := range refs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_pull_requests(task_id,project_id,repository,number,url,history_document_id)
			VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, taskID, projectID, ref.Repository, ref.Number, ref.URL, historyID); err != nil {
			return err
		}
	}
	// A pull request merged or closed before the task was finished was
	// reported by the repository's webhook already; take that state now.
	_, err := tx.ExecContext(ctx, `UPDATE task_pull_requests t SET state=g.state,merge_commit=g.merge_commit,merged_at=g.merged_at,checked_at=now(),check_error=NULL
		FROM github_pull_requests g WHERE t.task_id=$1 AND t.state='unknown' AND g.project_id=t.project_id AND g.repository=t.repository AND g.number=t.number`, taskID)
	return err
}

type deploymentScope struct {
	ProjectID string
	TaskID    string
	// UserID limits the check to tasks this user finished.
	UserID string
	// Force checks rows regardless of the last check time and age.
	Force bool
	Limit int
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// checkDeployments asks GitHub about unsettled pull requests and stores the
// result. GitHub is called outside any database transaction.
func (s *Store) checkDeployments(ctx context.Context, scope deploymentScope) (int, error) {
	tracked := ""
	if s.Deployment.Repository != "" && s.Deployment.Commit != "" {
		tracked = s.Deployment.Repository
	}
	// open/unknown rows are rechecked every deploymentRecheckAge, or every
	// webhookCoveredRecheck when a GitHub webhook reports the repository's pull
	// requests (the check then only catches missed deliveries); merged rows
	// in the server's repository only need one check per running build
	// (the deployed commit changes only when the server restarts).
	rows, err := many[TrackedPullRequest](ctx, s.DB, `SELECT to_jsonb(p) FROM task_pull_requests p
		WHERE p.state IN ('unknown','open','merged') AND p.recorded_state IS DISTINCT FROM p.state
		AND ($1='' OR p.project_id::text=$1) AND ($2='' OR p.task_id::text=$2)
		AND ($9='' OR p.task_id IN (SELECT id FROM task_runs WHERE created_by::text=$9))
		AND (p.state<>'merged' OR ($3<>'' AND p.repository=$3))
		AND ($4 OR (p.created_at>now()-make_interval(days=>$7) AND CASE WHEN p.state='merged'
			THEN p.checked_at IS NULL OR p.checked_at<$5
			ELSE p.checked_at IS NULL OR p.checked_at<now()-make_interval(mins=>CASE WHEN EXISTS(SELECT 1 FROM github_webhooks w
				WHERE w.project_id=p.project_id AND w.repository=p.repository AND w.enabled AND w.track_pull_requests) THEN $10::int ELSE $6::int END) END))
		ORDER BY p.checked_at NULLS FIRST, p.created_at LIMIT $8`,
		scope.ProjectID, scope.TaskID, tracked, scope.Force, processStarted, int(deploymentRecheckAge/time.Minute), deploymentWatchDays, scope.Limit, scope.UserID,
		int(webhookCoveredRecheck/time.Minute))
	if err != nil {
		return 0, err
	}
	gh := s.GitHub
	if gh == nil {
		gh = &GitHubClient{}
	}
	checked := 0
	for _, p := range rows {
		c, cancel := context.WithTimeout(ctx, 45*time.Second)
		next, err := s.checkPullRequest(c, gh, p)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return checked, ctx.Err()
			}
			code := errorCode(err)
			if _, err := s.DB.ExecContext(ctx, `UPDATE task_pull_requests SET checked_at=now(),check_error=$4 WHERE task_id=$1 AND repository=$2 AND number=$3`, p.TaskID, p.Repository, p.Number, clipRunes(code, 40)); err != nil {
				return checked, err
			}
			if code == "GITHUB_RATE_LIMITED" {
				break
			}
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE task_pull_requests SET state=$4,merge_commit=$5,merged_at=$6::timestamptz,deployed_commit=$7,
			deployed_at=CASE WHEN $4='deployed' THEN coalesce(deployed_at,now()) ELSE deployed_at END,checked_at=now(),check_error=NULL
			WHERE task_id=$1 AND repository=$2 AND number=$3 AND state<>'deployed'`,
			p.TaskID, p.Repository, p.Number, next.State, next.MergeCommit, next.MergedAt, next.DeployedCommit); err != nil {
			return checked, err
		}
		checked++
	}
	return checked, nil
}

func (s *Store) checkPullRequest(ctx context.Context, gh *GitHubClient, p TrackedPullRequest) (TrackedPullRequest, error) {
	pr, err := gh.PullRequest(ctx, p.Repository, p.Number)
	if err != nil {
		return p, err
	}
	p.MergeCommit, p.MergedAt, p.DeployedCommit = nil, nil, nil
	switch {
	case pr.Merged:
		p.State = "merged"
		if pr.MergeCommitSHA != nil && shaPattern.MatchString(*pr.MergeCommitSHA) {
			p.MergeCommit = pr.MergeCommitSHA
		}
		if pr.MergedAt != nil {
			if _, err := time.Parse(time.RFC3339, *pr.MergedAt); err == nil {
				p.MergedAt = pr.MergedAt
			}
		}
		if s.tracksDeployment(p.Repository) && p.MergeCommit != nil {
			ok, err := gh.Contains(ctx, p.Repository, s.Deployment.Commit, *p.MergeCommit)
			if err != nil {
				return p, err
			}
			if ok {
				p.State = "deployed"
				commit := s.Deployment.Commit
				p.DeployedCommit = &commit
			}
		}
	case pr.State == "closed":
		p.State = "closed"
	default:
		p.State = "open"
	}
	return p, nil
}

// recordDeployments prepends a status block to each history whose pull
// requests reached a final state that is not recorded yet. It returns the
// number of histories updated.
func (s *Store) recordDeployments(ctx context.Context, projectID, taskID string) (int, error) {
	tasks, err := many[string](ctx, s.DB, `SELECT to_jsonb(task_id) FROM (SELECT DISTINCT task_id FROM task_pull_requests
		WHERE state IN ('closed','merged','deployed') AND recorded_state IS DISTINCT FROM state AND history_document_id IS NOT NULL
		AND ($1='' OR project_id::text=$1) AND ($2='' OR task_id::text=$2) LIMIT 50) t`, projectID, taskID)
	if err != nil {
		return 0, err
	}
	recorded := 0
	for _, id := range tasks {
		var wrote bool
		err := transaction(ctx, s.DB, func(tx *sql.Tx) error {
			var err error
			wrote, err = s.recordTaskDeployment(ctx, tx, id)
			return err
		})
		if err != nil {
			return recorded, err
		}
		if wrote {
			recorded++
		}
	}
	return recorded, nil
}

func (s *Store) recordTaskDeployment(ctx context.Context, tx *sql.Tx, taskID string) (bool, error) {
	rows, err := many[TrackedPullRequest](ctx, tx, `SELECT to_jsonb(p)||jsonb_build_object('confirmed_by_name',(SELECT name FROM users WHERE id=p.confirmed_by))
		FROM task_pull_requests p WHERE task_id=$1 ORDER BY repository,number FOR UPDATE OF p`, taskID)
	if err != nil || len(rows) == 0 {
		return false, err
	}
	pending := false
	for _, p := range rows {
		if s.finalState(p) && (p.RecordedState == nil || *p.RecordedState != p.State) {
			pending = true
		}
	}
	if !pending || rows[0].HistoryDocumentID == nil {
		return false, nil
	}
	var creator string
	if err := tx.QueryRowContext(ctx, `SELECT created_by FROM task_runs WHERE id=$1`, taskID).Scan(&creator); err != nil {
		return false, err
	}
	doc, err := one[Document](ctx, tx, `SELECT to_jsonb(d) FROM documents d WHERE id=$1 AND project_id=$2 FOR UPDATE`, *rows[0].HistoryDocumentID, rows[0].ProjectID)
	if err != nil {
		return false, err
	}
	fail := func(code string) (bool, error) {
		_, err := tx.ExecContext(ctx, `UPDATE task_pull_requests SET check_error=$2 WHERE task_id=$1`, taskID, code)
		return false, err
	}
	content := insertStatusBlock(doc.Content, deploymentBlock(time.Now(), rows, s))
	if len([]rune(content)) > 100000 {
		return fail("HISTORY_TOO_LARGE")
	}
	// The block is written as the user who finished the task; a user who has
	// since lost edit access leaves the history unchanged.
	_, err = updateDocument(ctx, tx, Identity{User: User{ID: creator}}, UpdateInput{ProjectID: doc.ProjectID, DocumentID: doc.ID, ExpectedRevision: doc.Revision,
		Reason: "PR 병합·배포 상태 자동 기록", Patch: DocumentPatch{Content: &content}})
	if err != nil {
		var e *Error
		if errors.As(err, &e) && (e.Code == "FORBIDDEN" || e.Code == "NOT_FOUND") {
			return fail("HISTORY_NOT_WRITABLE")
		}
		return false, err
	}
	for _, p := range rows {
		if s.finalState(p) {
			if _, err := tx.ExecContext(ctx, `UPDATE task_pull_requests SET recorded_state=state,recorded_at=now(),check_error=NULL WHERE task_id=$1 AND repository=$2 AND number=$3`, taskID, p.Repository, p.Number); err != nil {
				return false, err
			}
		}
	}
	details := map[string]any{"task_id": taskID, "pull_requests": len(rows)}
	// The change landed: the linked to-do is done even if the task ended partial.
	if s.pullsLanded(rows) {
		todo, err := completeRunTodo(ctx, tx, taskID, creator, pullsDeployed(rows), true)
		if err != nil {
			return false, err
		}
		if todo != nil {
			details["todo_id"] = todo.ID
		}
	}
	return true, audit(ctx, tx, Event{Action: "deployment_recorded", ProjectID: doc.ProjectID, TargetID: doc.ID, Outcome: "success", Details: details})
}

func shortSHA(sha *string) string {
	if sha == nil {
		return ""
	}
	if len(*sha) > 7 {
		return (*sha)[:7]
	}
	return *sha
}

func seoulTime(raw *string) string {
	if raw == nil {
		return ""
	}
	t, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		return *raw
	}
	return t.In(seoul).Format("2006-01-02 15:04")
}

func (s *Store) describePullRequest(p TrackedPullRequest) string {
	if p.ConfirmedAt != nil && (p.State == "merged" || p.State == "closed") {
		who := "소유자·관리자"
		if p.ConfirmedByName != nil && *p.ConfirmedByName != "" {
			who = *p.ConfirmedByName
		}
		state := "병합"
		if p.State == "closed" {
			state = "병합되지 않고 닫힘"
		}
		return state + " — " + who + "이(가) 웹에서 수동 확인 " + seoulTime(p.ConfirmedAt) + "(GitHub가 이 서버에 알리지 않은 PR)"
	}
	merged := "병합"
	if p.MergedAt != nil {
		merged += " " + seoulTime(p.MergedAt)
	}
	if p.MergeCommit != nil {
		merged += ", 커밋 " + shortSHA(p.MergeCommit)
	}
	switch p.State {
	case "deployed":
		return merged + " → 운영 배포 확인: 실행 중인 서버 빌드 " + shortSHA(p.DeployedCommit) + "가 병합 커밋을 포함"
	case "merged":
		if s.tracksDeployment(p.Repository) {
			return merged + ". 실행 중인 서버 빌드에는 아직 포함되지 않음"
		}
		return merged + ". 이 저장소의 배포 여부는 서버가 확인하지 않는다"
	case "closed":
		return "병합되지 않고 닫힘"
	case "open":
		return "아직 열려 있음(미병합)"
	}
	return "아직 확인하지 못함"
}

// deploymentBlock renders the status block prepended to a history.
func deploymentBlock(now time.Time, rows []TrackedPullRequest, s *Store) string {
	var b strings.Builder
	manual, auto := 0, 0
	for _, p := range rows {
		if p.ConfirmedAt != nil {
			manual++
		} else {
			auto++
		}
	}
	source := "서버 자동 확인"
	switch {
	case manual > 0 && auto == 0:
		source = "웹 수동 확인"
	case manual > 0:
		source = "서버 자동 확인·웹 수동 확인"
	}
	fmt.Fprintf(&b, "> **상태 갱신 (%s Asia/Seoul, %s)** — 아래 본문은 작성 시점 기록이며 그대로 보존한다.\n", now.In(seoul).Format("2006-01-02 15:04"), source)
	for _, p := range rows {
		b.WriteString("> - PR " + p.URL + ": " + s.describePullRequest(p) + "\n")
	}
	b.WriteString("> - GitHub 병합 상태(웹훅 수신·조회 또는 수동 확인)와 실행 중인 서버 빌드만 확인했다. 운영에서 기능을 실제로 써 본 확인은 포함하지 않는다.\n")
	return b.String()
}

// insertStatusBlock puts block right below the leading "# title" line (above
// earlier status blocks) or at the top when there is no title line.
func insertStatusBlock(content, block string) string {
	if strings.HasPrefix(content, "# ") {
		title, rest, _ := strings.Cut(content, "\n")
		return title + "\n\n" + block + "\n" + strings.TrimLeft(rest, "\n")
	}
	return block + "\n" + content
}

// TaskDeployments lists the caller's pull requests whose state is not yet
// recorded in the task history.
type TaskDeployments struct {
	Pending  []TrackedPullRequest `json:"pending"`
	Server   DeploymentTarget     `json:"server"`
	Guidance string               `json:"guidance"`
}

func (s *Store) taskDeployments(ctx context.Context, tx *sql.Tx, userID, projectID string) (*TaskDeployments, error) {
	rows, err := many[TrackedPullRequest](ctx, tx, `SELECT to_jsonb(p)||jsonb_build_object('task_title',r.title) FROM task_pull_requests p
		JOIN task_runs r ON r.id=p.task_id WHERE p.project_id=$1 AND r.created_by=$2 AND p.recorded_state IS DISTINCT FROM p.state
		AND p.created_at>now()-make_interval(days=>$3) ORDER BY p.created_at DESC LIMIT 10`, projectID, userID, deploymentWatchDays)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return &TaskDeployments{Pending: rows, Server: s.Deployment, Guidance: "Pull requests from your earlier tasks whose merge or deployment is not yet recorded in their history. The server learns of merges from a GitHub webhook when one is set up, otherwise checks GitHub periodically, and prepends a status block to the history when a pull request is merged, deployed, or closed; call check_task_deployments to check now. Do not rewrite the original history body."}, nil
}

// RunDeploymentChecks checks unsettled pull requests shortly after start
// (a restart is usually a deployment) and then periodically.
func (s *Store) RunDeploymentChecks(ctx context.Context) {
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		c, cancel := context.WithTimeout(ctx, 5*time.Minute)
		if _, err := s.checkDeployments(c, deploymentScope{Limit: deploymentCheckBatch}); err != nil && c.Err() == nil {
			slog.Error("deployment check failed", "error", err)
		}
		if _, err := s.recordDeployments(c, "", ""); err != nil && c.Err() == nil {
			slog.Error("deployment record failed", "error", err)
		}
		cancel()
		timer.Reset(deploymentRecheckAge)
	}
}

type DeploymentCheckInput struct {
	ProjectID string `json:"project_id"`
	TaskID    string `json:"task_id,omitempty" jsonschema:"Only this task; omit for your tasks in the project"`
}

type DeploymentCheckResult struct {
	Checked           int                  `json:"checked"`
	RecordedHistories int                  `json:"recorded_histories"`
	Server            DeploymentTarget     `json:"server"`
	PullRequests      []TrackedPullRequest `json:"pull_requests"`
}

// PullRequestConfirmInput marks a tracked pull request merged or closed by
// hand, for one GitHub never reported to this server (merged before the
// repository's webhook was registered, or in a private repository the server
// cannot read).
type PullRequestConfirmInput struct {
	ProjectID  string `json:"project_id"`
	TaskID     string `json:"task_id"`
	Repository string `json:"repository" jsonschema:"owner/name as tracked"`
	Number     int    `json:"number"`
	State      string `json:"state" jsonschema:"merged or closed"`
}

// confirmPullRequest is web console only (a signed-in person, never an MCP
// key) and needs project owner or admin access. Only pull requests the server
// could not settle (unknown or open) can be confirmed.
func (s *Store) confirmPullRequest(ctx context.Context, tx *sql.Tx, a Identity, in PullRequestConfirmInput) (TrackedPullRequest, error) {
	if a.KeyID != "" {
		return TrackedPullRequest{}, problem("FORBIDDEN", "Pull requests are confirmed by a signed-in person in the web console.")
	}
	p, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, true)
	if err != nil {
		return TrackedPullRequest{}, err
	}
	if p.Role != "owner" && p.Role != "admin" {
		return TrackedPullRequest{}, problem("FORBIDDEN", "Owner or admin access is required.")
	}
	if in.State != "merged" && in.State != "closed" {
		return TrackedPullRequest{}, problem("INVALID_INPUT", "state must be merged or closed.")
	}
	repo := strings.ToLower(strings.TrimSpace(in.Repository))
	var current string
	err = tx.QueryRowContext(ctx, `SELECT state FROM task_pull_requests WHERE project_id=$1 AND task_id::text=$2 AND repository=$3 AND number=$4 FOR UPDATE`,
		p.ID, in.TaskID, repo, in.Number).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return TrackedPullRequest{}, problem("NOT_FOUND", "Pull request not tracked by this task.")
	}
	if err != nil {
		return TrackedPullRequest{}, err
	}
	if current != "unknown" && current != "open" {
		return TrackedPullRequest{}, problem("CONFLICT", "GitHub already settled this pull request as "+current+".")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE task_pull_requests SET state=$5,merge_commit=NULL,merged_at=NULL,checked_at=now(),check_error=NULL,confirmed_by=$6,confirmed_at=now()
		WHERE project_id=$1 AND task_id=$2 AND repository=$3 AND number=$4`, p.ID, in.TaskID, repo, in.Number, in.State, a.User.ID); err != nil {
		return TrackedPullRequest{}, err
	}
	return one[TrackedPullRequest](ctx, tx, `SELECT to_jsonb(t)||jsonb_build_object('confirmed_by_name',u.name) FROM task_pull_requests t LEFT JOIN users u ON u.id=t.confirmed_by
		WHERE t.task_id=$1 AND t.repository=$2 AND t.number=$3`, in.TaskID, repo, in.Number)
}

func registerDeploymentTools(t Tools, s *Store) {
	addCall(t, "confirm_pull_request", "Web console only, project owner or admin: mark a tracked pull request merged or closed by hand when GitHub never reported it (merged before the repository's webhook existed). The task history gets a status block saying it was confirmed by hand.", true, func(ctx context.Context, a Identity, in PullRequestConfirmInput) (any, error) {
		ctx = auditInput(ctx, in)
		out, err := s.Perform(ctx, a, "confirm_pull_request", in.ProjectID, true, false, func(tx *sql.Tx) (any, error) { return s.confirmPullRequest(ctx, tx, a, in) })
		if err != nil {
			return nil, err
		}
		if _, err := s.recordDeployments(ctx, in.ProjectID, in.TaskID); err != nil {
			return nil, err
		}
		return out, nil
	})
	// A person confirms what they saw on GitHub; an AI client's key cannot.
	tool := t["confirm_pull_request"]
	tool.Register = func(*mcp.Server, Identity) {}
	t["confirm_pull_request"] = tool

	addCall(t, "check_task_deployments", "Check now on GitHub whether the pull requests named in finish_task were merged, closed, or (for this server's own repository) deployed, and prepend a status block to each history whose pull requests reached a final state. The original history body is kept. Without task_id it checks your tasks in the project. Editor access required.", true, s.CheckTaskDeployments)
}

func (s *Store) CheckTaskDeployments(ctx context.Context, a Identity, in DeploymentCheckInput) (any, error) {
	ctx = auditInput(ctx, in)
	authorize := func(tx *sql.Tx) error {
		if _, err := projectAccess(ctx, tx, a.User.ID, in.ProjectID, true); err != nil {
			return err
		}
		if in.TaskID == "" {
			return nil
		}
		if err := validID(in.TaskID); err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM task_runs WHERE id=$1 AND project_id=$2)`, in.TaskID, in.ProjectID).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return problem("NOT_FOUND", "Task not found.")
		}
		return nil
	}
	// Authorize before calling GitHub so viewers and read-only keys cannot use
	// the server's token or rate limit.
	if _, err := s.Perform(ctx, a, "check_task_deployments_check", in.ProjectID, true, false, func(tx *sql.Tx) (any, error) { return nil, authorize(tx) }); err != nil {
		return nil, err
	}
	result := DeploymentCheckResult{Server: s.Deployment}
	// Without task_id only the caller's tasks are checked. At most
	// deploymentCheckBatch pull requests per call bounds GitHub usage.
	scope := deploymentScope{ProjectID: in.ProjectID, TaskID: in.TaskID, Force: true, Limit: deploymentCheckBatch}
	if in.TaskID == "" {
		scope.UserID = a.User.ID
	}
	var err error
	if result.Checked, err = s.checkDeployments(ctx, scope); err != nil {
		return nil, err
	}
	// Recording only writes states already confirmed on GitHub, so it may
	// cover every task in the scope.
	if result.RecordedHistories, err = s.recordDeployments(ctx, in.ProjectID, in.TaskID); err != nil {
		return nil, err
	}
	out, err := s.Perform(ctx, a, "check_task_deployments", in.ProjectID, true, false, func(tx *sql.Tx) (any, error) {
		if err := authorize(tx); err != nil {
			return nil, err
		}
		return many[TrackedPullRequest](ctx, tx, `SELECT to_jsonb(p)||jsonb_build_object('task_title',r.title) FROM task_pull_requests p JOIN task_runs r ON r.id=p.task_id
			WHERE p.project_id=$1 AND ($2='' OR p.task_id::text=$2) AND ($2<>'' OR r.created_by=$3) ORDER BY p.created_at DESC LIMIT 50`, in.ProjectID, in.TaskID, a.User.ID)
	})
	if err != nil {
		return nil, err
	}
	result.PullRequests = out.([]TrackedPullRequest)
	return result, nil
}
