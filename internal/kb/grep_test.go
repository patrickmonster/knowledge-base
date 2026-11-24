package kb

import (
	"strings"
	"testing"
)

func TestGrepMatcher(t *testing.T) {
	re, err := grepMatcher(GrepInput{Pattern: "a.b(c)"})
	if err != nil || !re.MatchString("x A.B(C) y") || re.MatchString("axb(c)") {
		t.Fatalf("fixed strings must be literal and case-insensitive: %v", err)
	}
	re, err = grepMatcher(GrepInput{Pattern: "Start_Task", CaseSensitive: true})
	if err != nil || re.MatchString("start_task") || !re.MatchString("call Start_Task(") {
		t.Fatalf("case_sensitive must match case exactly: %v", err)
	}
	re, err = grepMatcher(GrepInput{Pattern: `^##\s+결정`, Regex: true})
	if err != nil || !re.MatchString("## 결정과 이유") || re.MatchString("본문 ## 결정") {
		t.Fatalf("regex mode: %v", err)
	}
	for _, bad := range []GrepInput{
		{Pattern: ""},
		{Pattern: "   "},
		{Pattern: strings.Repeat("가", grepMaxPattern+1)},
		{Pattern: "a\nb"},
		{Pattern: "(unclosed", Regex: true},
		{Pattern: `(?<=x)y`, Regex: true}, // lookbehind is not RE2
	} {
		if _, err := grepMatcher(bad); errorCode(err) != "INVALID_INPUT" {
			t.Fatalf("pattern %q accepted: %v", bad.Pattern, err)
		}
	}
}

func TestLikePattern(t *testing.T) {
	if got := likePattern(`100%_a\b`); got != `%100\%\_a\\b%` {
		t.Fatalf("likePattern escaped wrongly: %q", got)
	}
}

func TestGrepLines(t *testing.T) {
	re, _ := grepMatcher(GrepInput{Pattern: "hit"})
	content := "one\r\nhit two\nthree\nfour hit\nfive\nhit six\nseven"
	count, matches := grepLines(re, content, 1, 2)
	if count != 3 || len(matches) != 2 {
		t.Fatalf("count=%d matches=%d", count, len(matches))
	}
	first := matches[0]
	if first.Line != 2 || first.Text != "hit two" || len(first.Before) != 1 || first.Before[0] != "one" || len(first.After) != 1 || first.After[0] != "three" {
		t.Fatalf("first match: %+v", first)
	}
	if matches[1].Line != 4 {
		t.Fatalf("second match line: %+v", matches[1])
	}
	if count, matches = grepLines(re, "hit at top", 3, 5); count != 1 || matches[0].Before != nil || matches[0].After != nil {
		t.Fatalf("context must stop at document edges: %+v", matches)
	}
}

func TestClipLine(t *testing.T) {
	long := strings.Repeat("가", 1000) + "TARGET" + strings.Repeat("나", 1000)
	at := strings.Index(long, "TARGET")
	got := clipLine(long, at)
	if !strings.Contains(got, "TARGET") || !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("clipped line must keep the match in view: %q", got)
	}
	if n := len([]rune(got)); n != grepMaxLineRunes+2 {
		t.Fatalf("clipped length %d", n)
	}
	if got := clipLine(long, len(long)-1); strings.HasSuffix(got, "…") || !strings.HasPrefix(got, "…") {
		t.Fatal("a match near the end must show the line end")
	}
	if clipLine("short", 0) != "short" {
		t.Fatal("short lines stay as they are")
	}
}

func TestGrepDocuments(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	outsider := inviteAs(t, s, admin, "grep-outsider@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "grep", Name: "grep"}).(Project)
	create := func(title, kind, status, content string, tags ...string) Document {
		return mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: title, Kind: kind, Status: status, Content: content, Tags: tags}).(Document)
	}
	ops := create("운영 참고", "reference", "", "# 운영\n배포 전 KB_API_KEY 를 확인한다.\n그다음 start_task( 호출\n끝", "운영")
	hist := create("작업 이력", "history", "", "kb_api_key 회전 기록\n100% 완료", "작업이력")
	draft := create("초안", "brief", "draft", "KB_API_KEY 초안 언급")
	create("무관", "reference", "", "검색되지 않는 문서")
	grep := func(in GrepInput) GrepResult {
		t.Helper()
		in.ProjectID = p.ID
		return mustCall(t, tools, admin, "grep_documents", in).(GrepResult)
	}
	ids := func(r GrepResult) map[string]GrepDocument {
		out := map[string]GrepDocument{}
		for _, d := range r.Documents {
			out[d.DocumentID] = d
		}
		return out
	}

	r := grep(GrepInput{Pattern: "KB_API_KEY"})
	got := ids(r)
	if len(got) != 2 || got[ops.ID].MatchCount != 1 || got[hist.ID].MatchCount != 1 {
		t.Fatalf("case-insensitive fixed search over active documents: %+v", r.Documents)
	}
	if m := got[ops.ID].Matches[0]; m.Line != 2 || !strings.Contains(m.Text, "KB_API_KEY") {
		t.Fatalf("line number and text: %+v", m)
	}
	if !strings.HasPrefix(got[ops.ID].Citation, "kb://"+p.ID+"/documents/"+ops.ID+"?revision=") {
		t.Fatalf("citation: %s", got[ops.ID].Citation)
	}
	if r.Status != "active" || r.ScanTruncated || r.HasMore {
		t.Fatalf("result flags: %+v", r)
	}

	if got = ids(grep(GrepInput{Pattern: "KB_API_KEY", CaseSensitive: true})); len(got) != 1 || got[ops.ID].DocumentID == "" {
		t.Fatalf("case_sensitive: %+v", got)
	}
	if got = ids(grep(GrepInput{Pattern: "KB_API_KEY", Status: "draft"})); len(got) != 1 || got[draft.ID].DocumentID == "" {
		t.Fatalf("status filter: %+v", got)
	}
	if got = ids(grep(GrepInput{Pattern: "kb_api_key", Kind: "history"})); len(got) != 1 || got[hist.ID].DocumentID == "" {
		t.Fatalf("kind filter: %+v", got)
	}
	if got = ids(grep(GrepInput{Pattern: "kb_api_key", Tags: []string{"운영"}})); len(got) != 1 || got[ops.ID].DocumentID == "" {
		t.Fatalf("tag filter: %+v", got)
	}
	// LIKE metacharacters are literal: "100%" must not match "100 anything".
	if got = ids(grep(GrepInput{Pattern: "100%"})); len(got) != 1 || got[hist.ID].DocumentID == "" {
		t.Fatalf("LIKE escaping: %+v", got)
	}
	if got = ids(grep(GrepInput{Pattern: "start_task("})); len(got) != 1 || got[ops.ID].DocumentID == "" {
		t.Fatalf("punctuation in fixed strings: %+v", got)
	}
	// Regex is matched per line in Go, including anchors.
	r = grep(GrepInput{Pattern: `^#\s+운영$`, Regex: true, Context: 1})
	if got = ids(r); len(got) != 1 || got[ops.ID].Matches[0].Line != 1 || len(got[ops.ID].Matches[0].After) != 1 {
		t.Fatalf("regex with context: %+v", r.Documents)
	}
	// Title-only matches are reported without lines.
	if got = ids(grep(GrepInput{Pattern: "작업 이력"})); got[hist.ID].DocumentID == "" || !got[hist.ID].TitleMatch || got[hist.ID].MatchCount != 0 {
		t.Fatalf("title match: %+v", got)
	}

	// Paging walks matching documents newest first.
	page1 := grep(GrepInput{Pattern: "kb_api_key", Limit: 1})
	if len(page1.Documents) != 1 || !page1.HasMore || page1.NextOffset == nil || *page1.NextOffset != 1 || page1.Documents[0].DocumentID != hist.ID {
		t.Fatalf("page 1: %+v", page1)
	}
	page2 := grep(GrepInput{Pattern: "kb_api_key", Limit: 1, Offset: 1})
	if len(page2.Documents) != 1 || page2.HasMore || page2.Documents[0].DocumentID != ops.ID {
		t.Fatalf("page 2: %+v", page2)
	}

	for _, bad := range []GrepInput{
		{Pattern: "x", Limit: grepMaxLimit + 1},
		{Pattern: "x", Context: grepMaxContext + 1},
		{Pattern: "x", MaxMatches: grepMaxMatches + 1},
		{Pattern: "x", Status: "deleted"},
		{Pattern: "x", Kind: "memo"},
		{Pattern: "(", Regex: true},
	} {
		bad.ProjectID = p.ID
		_, err := call(t, tools, admin, "grep_documents", bad)
		denied(t, err, "INVALID_INPUT")
	}
	_, err := call(t, tools, outsider, "grep_documents", GrepInput{ProjectID: p.ID, Pattern: "KB_API_KEY"})
	denied(t, err, "NOT_FOUND")
}
