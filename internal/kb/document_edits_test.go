package kb

import (
	"strings"
	"testing"
)

func TestApplyDocumentEdits(t *testing.T) {
	got, err := applyDocumentEdits("가 나 다\n라 마", []DocumentEdit{{OldStr: "나", NewStr: "뺐다"}, {OldStr: "뺐다 다", NewStr: "끈다"}, {OldStr: "\n라 ", NewStr: ""}})
	if err != nil || got != "가 끈다마" {
		t.Fatalf("sequential edits: %q %v", got, err)
	}
	for _, c := range []struct {
		name    string
		content string
		edits   []DocumentEdit
		code    string
		msg     string
	}{
		{"missing", "abc", []DocumentEdit{{OldStr: "x", NewStr: "y"}}, "EDIT_NOT_APPLIED", "edits[0].old_str was not found"},
		{"duplicate", "a b a", []DocumentEdit{{OldStr: "a", NewStr: "c"}}, "EDIT_NOT_APPLIED", "more than once (2 times)"},
		{"overlapping", "aaa", []DocumentEdit{{OldStr: "aa", NewStr: "b"}}, "EDIT_NOT_APPLIED", "more than once (2 times)"},
		{"later edit fails", "abc", []DocumentEdit{{OldStr: "a", NewStr: "z"}, {OldStr: "a", NewStr: "y"}}, "EDIT_NOT_APPLIED", "edits[1]"},
		{"empty old", "abc", []DocumentEdit{{OldStr: "", NewStr: "y"}}, "INVALID_INPUT", "edits[0].old_str"},
		{"long old", "abc", []DocumentEdit{{OldStr: strings.Repeat("가", maxDocumentEditChars+1)}}, "INVALID_INPUT", "edits[0].old_str"},
		{"long new", "abc", []DocumentEdit{{OldStr: "a", NewStr: strings.Repeat("가", maxDocumentEditChars+1)}}, "INVALID_INPUT", "edits[0].new_str"},
		{"too many", "abc", make([]DocumentEdit, maxDocumentEdits+1), "INVALID_INPUT", "At most"},
	} {
		_, err := applyDocumentEdits(c.content, c.edits)
		if err == nil || errorCode(err) != c.code || !strings.Contains(err.Error(), c.msg) {
			t.Fatalf("%s: want %s containing %q, got %v", c.name, c.code, c.msg, err)
		}
	}
}

func TestUpdateDocumentEdits(t *testing.T) {
	s := testStore(t)
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, admin, "create_project", CreateProjectInput{Slug: "edits", Name: "edits"}).(Project)
	d := mustCall(t, tools, admin, "create_document", DocumentInput{ProjectID: p.ID, Title: "개요", Kind: "reference", Content: "첫 문장. 둘째 문장. 셋째 문장."}).(Document)

	title := "개요 v2"
	out := mustCall(t, tools, admin, "update_document", UpdateInput{ProjectID: p.ID, DocumentID: d.ID, ExpectedRevision: 1, Reason: "오타 수정",
		Patch: DocumentPatch{Title: &title}, Edits: []DocumentEdit{{OldStr: "둘째 문장.", NewStr: "고친 문장."}, {OldStr: " 셋째 문장.", NewStr: ""}}}).(EditedDocument)
	if out.EditsApplied != 2 || out.Revision != 2 || out.Content != "첫 문장. 고친 문장." || out.Title != title {
		t.Fatalf("edited: %+v", out)
	}

	unchanged := func() {
		t.Helper()
		cur := mustCall(t, tools, admin, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: d.ID}).(Document)
		if cur.Revision != 2 || cur.Content != "첫 문장. 고친 문장." {
			t.Fatalf("document changed: rev %d %q", cur.Revision, cur.Content)
		}
	}
	body := "새 본문"
	for _, c := range []struct {
		in   UpdateInput
		code string
	}{
		{UpdateInput{ExpectedRevision: 2, Edits: []DocumentEdit{{OldStr: "고친", NewStr: "x"}, {OldStr: "없는 글", NewStr: "y"}}}, "EDIT_NOT_APPLIED"},
		{UpdateInput{ExpectedRevision: 2, Edits: []DocumentEdit{{OldStr: "문장", NewStr: "x"}}}, "EDIT_NOT_APPLIED"},
		{UpdateInput{ExpectedRevision: 2, Patch: DocumentPatch{Content: &body}, Edits: []DocumentEdit{{OldStr: "첫", NewStr: "x"}}}, "INVALID_INPUT"},
		{UpdateInput{ExpectedRevision: 1, Edits: []DocumentEdit{{OldStr: "첫", NewStr: "x"}}}, "REVISION_CONFLICT"},
		{UpdateInput{ExpectedRevision: 2, Edits: []DocumentEdit{{OldStr: "첫 문장. 고친 문장.", NewStr: ""}}}, "INVALID_INPUT"},
	} {
		c.in.ProjectID, c.in.DocumentID, c.in.Reason = p.ID, d.ID, "시험"
		_, err := call(t, tools, admin, "update_document", c.in)
		denied(t, err, c.code)
		unchanged()
	}

	// Without edits the response stays a plain Document.
	if _, ok := mustCall(t, tools, admin, "update_document", UpdateInput{ProjectID: p.ID, DocumentID: d.ID, ExpectedRevision: 2, Reason: "전체 교체", Patch: DocumentPatch{Content: &body}}).(Document); !ok {
		t.Fatal("full replacement should return Document")
	}
}
