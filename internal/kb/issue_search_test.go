package kb

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestIssueIndexText(t *testing.T) {
	got := issueIndexText(" 본문 kb://x/documents/y ", "", "")
	if got != "본문" {
		t.Fatalf("body only: %q", got)
	}
	got = issueIndexText("본문", "답변 내용", "해결 메모")
	if !strings.Contains(got, "## 답변\n\n답변 내용") || !strings.Contains(got, "## 해결\n\n해결 메모") || !strings.HasPrefix(got, "본문") {
		t.Fatalf("answer and resolution: %q", got)
	}
}

func TestFuseIssueRanks(t *testing.T) {
	a, b, c := IssueSearchHit{IssueID: "a"}, IssueSearchHit{IssueID: "b"}, IssueSearchHit{IssueID: "c"}
	out := fuseIssueRanks([]IssueSearchHit{b, c}, []IssueSearchHit{a, b}, 2)
	if len(out) != 2 || out[0].IssueID != "b" || out[1].IssueID != "a" || out[0].Score <= out[1].Score {
		t.Fatalf("fused order: %+v", out)
	}
}

func TestIssueSemanticSearch(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "issue-search", Name: "issue search"}).(Project)
	mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "배포 절차", Kind: "reference", Content: "배포 전에 마이그레이션을 확인합니다."})
	refund := mustCall(t, tools, admin, "create_issue", CreateIssueInput{ProjectID: p.ID, Title: "결제 취소 정책", Body: "환불은 며칠 안에 처리되나요?"}).(Issue)
	logs := mustCall(t, tools, admin, "create_issue", CreateIssueInput{ProjectID: p.ID, Kind: "issue", Title: "로그 레벨", Body: "운영 로그 설정을 바꿔야 합니다."}).(Issue)
	closed := mustCall(t, tools, admin, "create_issue", CreateIssueInput{ProjectID: p.ID, Title: "환불 문의 철회", Body: "환불 질문은 취소합니다."}).(Issue)
	mustCall(t, tools, admin, "update_issue", UpdateIssueInput{ProjectID: p.ID, IssueID: closed.ID, Status: "closed"})

	chunks := func(issueID string) (n int, pending int) {
		t.Helper()
		if err := s.DB.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE embedding_status='pending') FROM issue_chunks WHERE issue_id=$1`, issueID).Scan(&n, &pending); err != nil {
			t.Fatal(err)
		}
		return
	}
	if n, _ := chunks(refund.ID); n != 1 {
		t.Fatalf("create_issue should chunk the question, got %d chunks", n)
	}

	// Questions saved before issue chunks existed are chunked by the worker.
	legacy := uuid.NewString()
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO issues(id,project_id,kind,title,body,created_by) VALUES($1,$2,'question','오래된 질문','환불 계좌는 어디서 바꾸나요?',$3)`, legacy, p.ID, admin.User.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := chunks(legacy); n != 0 {
		t.Fatal("legacy question should start without chunks")
	}
	status := mustCall(t, tools, admin, "get_embedding_status", ProjectInput{ProjectID: p.ID}).(map[string]any)
	if is := status["issues"].(map[string]any); is["unchunked"].(float64) != 1 || is["total"].(float64) != 3 {
		t.Fatalf("issue embedding status before worker: %+v", is)
	}

	// Lexical search lists questions next to documents without an embedder.
	lexical := mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "로그", Mode: "lexical"}).(SearchResult)
	if len(lexical.Issues) != 1 || lexical.Issues[0].IssueID != logs.ID || lexical.Issues[0].Kind != "issue" ||
		lexical.Issues[0].Citation != "issue://"+p.ID+"/"+logs.ID {
		t.Fatalf("lexical issue hits: %+v", lexical.Issues)
	}

	e := &testEmbedder{model: "test-local"}
	s.Embedder = e
	// Production parses ISSUE_SCORE_FLOOR into Store.Search; a bare Store has
	// no floor, so set the built-in default to exercise it here.
	s.Search = SearchPolicy{IssueScoreFloor: defaultIssueScoreFloor}
	for i := 0; i < 5; i++ {
		n, err := s.ProcessIssueEmbeddingBatch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	if n, pending := chunks(legacy); n != 1 || pending != 0 {
		t.Fatalf("legacy question chunks=%d pending=%d", n, pending)
	}
	status = mustCall(t, tools, admin, "get_embedding_status", ProjectInput{ProjectID: p.ID}).(map[string]any)
	if is := status["issues"].(map[string]any); is["ready"].(float64) != 4 || is["unchunked"].(float64) != 0 || is["pending"].(float64) != 0 {
		t.Fatalf("issue embedding status after worker: %+v", is)
	}

	// A paraphrase with no shared words finds the refund questions by meaning;
	// the closed one is left out.
	semantic := mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "돈을 돌려받는 방법", Mode: "semantic"}).(SearchResult)
	if len(semantic.Issues) < 2 || semantic.UnindexedIssueChunks != 0 {
		t.Fatalf("semantic issue hits: %+v", semantic)
	}
	top := map[string]bool{semantic.Issues[0].IssueID: true, semantic.Issues[1].IssueID: true}
	if !top[refund.ID] || !top[legacy] {
		t.Fatalf("refund questions should rank first: %+v", semantic.Issues)
	}
	for _, h := range semantic.Issues {
		if h.IssueID == closed.ID {
			t.Fatal("closed question returned")
		}
	}
	hybrid := mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "돈을 돌려받는 방법"}).(SearchResult)
	if hybrid.Retrieval != "hybrid" || len(hybrid.Issues) == 0 || !top[hybrid.Issues[0].IssueID] {
		t.Fatalf("hybrid issue hits: %+v", hybrid.Issues)
	}

	// The score floor keeps unrelated questions out of semantic and hybrid
	// results and reports how many it removed.
	if len(semantic.Issues) != 2 || semantic.FilteredIssues != 1 {
		t.Fatalf("floor on semantic results: %d hits, %d filtered", len(semantic.Issues), semantic.FilteredIssues)
	}
	for _, h := range semantic.Issues {
		if h.Score < defaultIssueScoreFloor {
			t.Fatalf("hit below the floor: %+v", h)
		}
	}
	// Keyword results have no vector score and are never filtered this way.
	if lexical.FilteredIssues != 0 {
		t.Fatalf("lexical results filtered: %+v", lexical)
	}
	// 0 turns the floor off and the weak match comes back.
	if _, err := s.SetSearchSettings(ctx, admin, SearchSettingsInput{IssueScoreFloor: floatp(0)}); err != nil {
		t.Fatal(err)
	}
	unfiltered := mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "돈을 돌려받는 방법", Mode: "semantic"}).(SearchResult)
	if len(unfiltered.Issues) != 3 || unfiltered.FilteredIssues != 0 {
		t.Fatalf("floor off: %d hits, %d filtered", len(unfiltered.Issues), unfiltered.FilteredIssues)
	}
	if _, err := s.SetSearchSettings(ctx, admin, SearchSettingsInput{}); err != nil {
		t.Fatal(err)
	}

	// Answering rebuilds the chunks, so the answer is searchable and re-embedded.
	mustCall(t, tools, admin, "answer_issue", AnswerIssueInput{ProjectID: p.ID, IssueID: logs.ID, Answer: "DEBUG 레벨은 장애 때만 켭니다."})
	if n, pending := chunks(logs.ID); n != 1 || pending != 1 {
		t.Fatalf("answer should requeue chunks: n=%d pending=%d", n, pending)
	}
	answered := mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "DEBUG", Mode: "lexical"}).(SearchResult)
	if len(answered.Issues) != 1 || answered.Issues[0].IssueID != logs.ID || answered.Issues[0].Status != "answered" {
		t.Fatalf("answer text search: %+v", answered.Issues)
	}
	pendingSearch := mustCall(t, tools, admin, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "로그", Mode: "semantic"}).(SearchResult)
	if pendingSearch.UnindexedIssueChunks != 1 {
		t.Fatalf("pending issue chunks not reported: %+v", pendingSearch)
	}

	// Task context keeps its own related-question list and never adds issue hits.
	var ctxOut SearchResult
	err := transaction(ctx, s.DB, func(tx *sql.Tx) error {
		var err error
		ctxOut, err = s.searchCurrentKnowledge(ctx, tx, admin, SearchInput{ProjectID: p.ID, Query: "환불", Mode: "lexical"}, searchSourceTask)
		return err
	})
	if err != nil || len(ctxOut.Issues) != 0 {
		t.Fatalf("internal search should not include issues: %+v %v", ctxOut.Issues, err)
	}

	// Reindex rebuilds question chunks too.
	re := mustCall(t, tools, admin, "reindex_project_embeddings", ProjectInput{ProjectID: p.ID}).(map[string]any)
	if re["queued_issue_chunks"].(int64) != 4 {
		t.Fatalf("reindex: %+v", re)
	}
}
