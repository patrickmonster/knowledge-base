package kb

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type failingExtractor struct{}

func (failingExtractor) Extract(context.Context, string, string) (ExtractResult, error) {
	return ExtractResult{}, errors.New("parser crashed")
}

func TestReextractAttachments(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	objects := &memoryObjects{files: map[string][]byte{}}
	s.Objects = objects
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	owner := inviteAs(t, s, admin, "owner@example.com")
	viewer := inviteAs(t, s, admin, "viewer@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "reextract", Name: "Reextract"}).(Project)
	mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})
	upload := func(name string, body []byte) Attachment {
		t.Helper()
		out := mustCall(t, tools, owner, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: p.ID, Filename: name, SizeBytes: int64(len(body))}).(map[string]any)
		stored, err := s.UploadAttachment(ctx, owner, AttachmentInput{p.ID, out["id"].(string)}, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return stored.(Attachment)
	}
	get := func(id string) Attachment {
		t.Helper()
		for _, f := range mustCall(t, tools, owner, "list_attachments", ListAttachmentsInput{ProjectID: p.ID}).([]Attachment) {
			if f.ID == id {
				return f
			}
		}
		t.Fatalf("attachment %s not listed", id)
		return Attachment{}
	}
	run := func(in ReextractInput) ReextractResult {
		t.Helper()
		in.ProjectID = p.ID
		return mustCall(t, tools, owner, "reextract_attachment", in).(ReextractResult)
	}

	// Files stored while extraction was off, plus one that cannot be parsed and a text file.
	s.Extractor = nil
	old1 := upload("환불 규정.docx", docxBytes(t, "결제 후 칠일 이내 전액 환불"))
	old2 := upload("배송 안내.docx", docxBytes(t, "택배는 영업일 기준 이틀 안에 출발"))
	broken := upload("깨진.xlsx", []byte("PK not a zip"))
	plain := upload("notes.txt", []byte("hello"))
	if old1.TextStatus != "none" || old2.TextStatus != "none" || broken.TextStatus != "none" {
		t.Fatalf("extraction was not off: %+v %+v %+v", old1, old2, broken)
	}

	// Nothing to do while the server has extraction turned off.
	_, err := call(t, tools, owner, "reextract_attachment", ReextractInput{ProjectID: p.ID})
	denied(t, err, "CONFLICT")
	s.Extractor = InProcessExtractor{}

	// Viewers cannot, text files and bad inputs are rejected.
	_, err = call(t, tools, viewer, "reextract_attachment", ReextractInput{ProjectID: p.ID, AttachmentID: old1.ID})
	denied(t, err, "FORBIDDEN")
	_, err = call(t, tools, owner, "reextract_attachment", ReextractInput{ProjectID: p.ID, AttachmentID: plain.ID})
	denied(t, err, "INVALID_INPUT")
	_, err = call(t, tools, owner, "reextract_attachment", ReextractInput{ProjectID: p.ID, Limit: 21})
	denied(t, err, "INVALID_INPUT")

	// A batch of one reports that more files wait; the next run finishes the rest.
	first := run(ReextractInput{Limit: 1})
	if len(first.Items) != 1 || !first.More || first.Items[0].Result != "extracted" {
		t.Fatalf("first batch: %+v", first)
	}
	rest := run(ReextractInput{})
	if len(rest.Items) != 2 || rest.More {
		t.Fatalf("second batch: %+v", rest)
	}
	got := map[string]ReextractItem{}
	for _, it := range append(first.Items, rest.Items...) {
		got[it.AttachmentID] = it
	}
	if got[old1.ID].Result != "extracted" || got[old2.ID].Result != "extracted" || got[broken.ID].Result != "failed" || got[broken.ID].TextError != ExtractInvalidFile {
		t.Fatalf("results: %+v", got)
	}
	if _, ok := got[plain.ID]; ok {
		t.Fatal("a text file was retried")
	}
	f1 := get(old1.ID)
	if f1.TextStatus != "extracted" || f1.TextDocumentID == nil {
		t.Fatalf("attachment not updated: %+v", f1)
	}
	doc := mustCall(t, tools, viewer, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: *f1.TextDocumentID}).(Document)
	if doc.Kind != "artifact" || doc.Status != "active" || doc.Revision != 1 || doc.CreatedBy != owner.User.ID || !strings.Contains(doc.Content, "전액 환불") {
		t.Fatalf("text document: %+v", doc)
	}
	hits := mustCall(t, tools, viewer, "search_knowledge", SearchInput{ProjectID: p.ID, Query: "전액 환불", Mode: "lexical"}).(SearchResult)
	if len(hits.Hits) == 0 || hits.Hits[0].DocumentID != doc.ID {
		t.Fatalf("re-extracted text not searchable: %+v", hits)
	}
	if b := get(broken.ID); b.TextStatus != "failed" || b.TextDocumentID != nil {
		t.Fatalf("broken file: %+v", b)
	}
	// A broken file is retried again, but behind files not tried yet.
	if again := run(ReextractInput{}); len(again.Items) != 1 || again.Items[0].AttachmentID != broken.ID || again.More {
		t.Fatalf("retry of failed file: %+v", again)
	}

	// Same text again changes nothing; stale text is replaced by a new revision.
	same := run(ReextractInput{AttachmentID: old1.ID}).Items[0]
	if same.Result != "unchanged" || mustCall(t, tools, viewer, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: doc.ID}).(Document).Revision != 1 {
		t.Fatalf("same text: %+v", same)
	}
	stale := "오래된 줄바꿈 깨진 본문"
	mustCall(t, tools, owner, "update_document", UpdateInput{ProjectID: p.ID, DocumentID: doc.ID, ExpectedRevision: 1, Reason: "시험", Patch: DocumentPatch{Content: &stale}})
	fixed := run(ReextractInput{AttachmentID: old1.ID}).Items[0]
	doc = mustCall(t, tools, viewer, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: doc.ID}).(Document)
	if fixed.Result != "extracted" || fixed.TextDocumentID != doc.ID || doc.Revision != 3 || !strings.Contains(doc.Content, "전액 환불") || strings.Contains(doc.Content, stale) {
		t.Fatalf("stale text not replaced: %+v rev %d", fixed, doc.Revision)
	}

	// A failing retry keeps the text that is already indexed.
	s.Extractor = failingExtractor{}
	kept := run(ReextractInput{AttachmentID: old1.ID}).Items[0]
	f1 = get(old1.ID)
	after := mustCall(t, tools, viewer, "get_document", DocumentRef{ProjectID: p.ID, DocumentID: doc.ID}).(Document)
	if kept.Result != "unchanged" || f1.TextStatus != "extracted" || f1.TextDocumentID == nil || *f1.TextDocumentID != doc.ID || after.Revision != doc.Revision || after.Status != "active" {
		t.Fatalf("failed retry damaged the text: %+v %+v rev %d", kept, f1, after.Revision)
	}
	s.Extractor = InProcessExtractor{}

	// A stored object that no longer matches its SHA-256 is never indexed.
	objects.files[attachmentKey(p.ID, old2.ID)] = docxBytes(t, "다른 파일로 바뀐 내용")
	_, err = call(t, tools, owner, "reextract_attachment", ReextractInput{ProjectID: p.ID, AttachmentID: old2.ID})
	denied(t, err, "CONFLICT")
}
