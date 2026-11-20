package kb

import (
	"bytes"
	"context"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIssueImageAttachments(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m := &memoryObjects{files: map[string][]byte{}}
	s.Objects = m
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	owner := inviteAs(t, s, admin, "owner@example.com")
	viewer := inviteAs(t, s, admin, "viewer@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "imgs", Name: "Images"}).(Project)
	other := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "imgs-other", Name: "Other"}).(Project)
	mustCall(t, tools, owner, "set_project_member", MemberInput{ProjectID: p.ID, UserID: viewer.User.ID, Role: "viewer"})
	issue := mustCall(t, tools, owner, "create_issue", CreateIssueInput{ProjectID: p.ID, Title: "화면 깨짐", Body: "스크린샷 첨부"}).(Issue)
	otherIssue := mustCall(t, tools, owner, "create_issue", CreateIssueInput{ProjectID: other.ID, Title: "다른 프로젝트", Body: "x"}).(Issue)

	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 40)
	prepare := func(in PrepareAttachmentInput) (Attachment, error) {
		out, err := call(t, tools, owner, "prepare_attachment_upload", in)
		if err != nil {
			return Attachment{}, err
		}
		return out.(map[string]any)["attachment"].(Attachment), nil
	}

	// Reservation rules: issue in the same project, image filename, 10 MiB, not both links.
	_, err := prepare(PrepareAttachmentInput{ProjectID: p.ID, IssueID: otherIssue.ID, Filename: "a.png", SizeBytes: 10})
	denied(t, err, "NOT_FOUND")
	_, err = prepare(PrepareAttachmentInput{ProjectID: p.ID, IssueID: issue.ID, Filename: "a.pdf", SizeBytes: 10})
	denied(t, err, "INVALID_INPUT")
	_, err = prepare(PrepareAttachmentInput{ProjectID: p.ID, IssueID: issue.ID, Filename: "a.svg", SizeBytes: 10})
	denied(t, err, "INVALID_INPUT")
	_, err = prepare(PrepareAttachmentInput{ProjectID: p.ID, IssueID: issue.ID, Filename: "a.png", SizeBytes: MaxIssueImageBytes + 1})
	denied(t, err, "INVALID_INPUT")
	doc := mustCall(t, tools, owner, "create_document", DocumentInput{ProjectID: p.ID, Title: "Doc", Kind: "brief", Content: "x"}).(Document)
	_, err = prepare(PrepareAttachmentInput{ProjectID: p.ID, IssueID: issue.ID, DocumentID: doc.ID, Filename: "a.png", SizeBytes: 10})
	denied(t, err, "INVALID_INPUT")
	_, err = call(t, tools, viewer, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: p.ID, IssueID: issue.ID, Filename: "a.png", SizeBytes: 10})
	denied(t, err, "FORBIDDEN")

	// Content must really be a raster image, not just named like one.
	fake, err := prepare(PrepareAttachmentInput{ProjectID: p.ID, IssueID: issue.ID, Filename: "fake.png", SizeBytes: 11})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.UploadAttachment(ctx, owner, AttachmentInput{p.ID, fake.ID}, strings.NewReader("<svg></svg>"))
	denied(t, err, "INVALID_INPUT")
	if m.puts != 0 {
		t.Fatal("non-image content reached the bucket")
	}

	img, err := prepare(PrepareAttachmentInput{ProjectID: p.ID, IssueID: issue.ID, Filename: "screen.png", SizeBytes: int64(len(png))})
	if err != nil {
		t.Fatal(err)
	}
	if img.IssueID == nil || *img.IssueID != issue.ID {
		t.Fatalf("issue link missing: %+v", img)
	}
	if _, err = s.UploadAttachment(ctx, owner, AttachmentInput{p.ID, img.ID}, strings.NewReader(png)); err != nil {
		t.Fatal(err)
	}
	full := mustCall(t, tools, viewer, "get_issue", IssueRef{ProjectID: p.ID, IssueID: issue.ID}).(Issue)
	if full.AttachmentCount != 1 || len(full.Attachments) != 1 || full.Attachments[0].ID != img.ID {
		t.Fatalf("get_issue attachments: count %d, %+v", full.AttachmentCount, full.Attachments)
	}
	listed := mustCall(t, tools, viewer, "list_attachments", ListAttachmentsInput{ProjectID: p.ID, IssueID: issue.ID}).([]Attachment)
	if len(listed) != 2 { // the ready image and the rejected reservation awaiting cleanup
		t.Fatalf("list by issue: %d", len(listed))
	}

	// Inline image route: session only, project members only, sniffed image type.
	app, err := NewApp(s, Config{PublicURL: "http://localhost:8080"})
	if err != nil {
		t.Fatal(err)
	}
	handler := app.Handler()
	get := func(email, projectID, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/attachments/"+id+"/image?project_id="+projectID, nil)
		if email != "" {
			cookie, err := s.Login(ctx, email, testPassword)
			if err != nil {
				t.Fatal(err)
			}
			r.AddCookie(&http.Cookie{Name: "kb_session", Value: cookie})
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	if w := get("", p.ID, img.ID); w.Code != 401 {
		t.Fatalf("anonymous image read: %d", w.Code)
	}
	if w := get("admin@example.com", p.ID, img.ID); w.Code != 404 {
		t.Fatalf("non-member image read: %d", w.Code)
	}
	if w := get("viewer@example.com", other.ID, img.ID); w.Code == 200 {
		t.Fatal("image served under another project")
	}
	w := get("viewer@example.com", p.ID, img.ID)
	if w.Code != 200 || w.Body.String() != png || w.Header().Get("Content-Type") != "image/png" ||
		w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("image response: %d %q %v", w.Code, w.Header().Get("Content-Type"), w.Header())
	}
	// ?size=thumb on a small image serves the original and stores nothing.
	thumbAs := func(email, id string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/attachments/"+id+"/image?size=thumb&project_id="+p.ID, nil)
		cookie, err := s.Login(ctx, email, testPassword)
		if err != nil {
			t.Fatal(err)
		}
		r.AddCookie(&http.Cookie{Name: "kb_session", Value: cookie})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	thumbGet := func(id string) *httptest.ResponseRecorder { return thumbAs("viewer@example.com", id) }
	puts := m.puts
	if w := thumbGet(img.ID); w.Code != 200 || w.Body.String() != png || m.puts != puts {
		t.Fatalf("thumb of tiny image: %d", w.Code)
	}
	// A large photo gets a small JPEG copy, stored once and reused.
	bigPNG := encodePNG(t, solid(1600, 1200, color.RGBA{10, 120, 200, 255}))
	big, err := prepare(PrepareAttachmentInput{ProjectID: p.ID, IssueID: issue.ID, Filename: "photo.png", SizeBytes: int64(len(bigPNG))})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UploadAttachment(ctx, owner, AttachmentInput{p.ID, big.ID}, bytes.NewReader(bigPNG)); err != nil {
		t.Fatal(err)
	}
	tw := thumbGet(big.ID)
	if tw.Code != 200 || tw.Header().Get("Content-Type") != "image/jpeg" || !strings.Contains(tw.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("thumb response: %d %v", tw.Code, tw.Header())
	}
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(tw.Body.Bytes())); err != nil || cfg.Width != 480 || cfg.Height != 360 {
		t.Fatalf("thumb size: %v %dx%d", err, cfg.Width, cfg.Height)
	}
	stored, ok := m.files[thumbnailKey(p.ID, big.ID)]
	if !ok || !bytes.Equal(stored, tw.Body.Bytes()) {
		t.Fatal("thumbnail not stored")
	}
	puts = m.puts
	if w2 := thumbGet(big.ID); w2.Code != 200 || !bytes.Equal(w2.Body.Bytes(), stored) || m.puts != puts {
		t.Fatal("stored thumbnail not reused")
	}
	if w := get("viewer@example.com", p.ID, big.ID); w.Code != 200 || !bytes.Equal(w.Body.Bytes(), bigPNG) {
		t.Fatal("original changed")
	}
	if w := thumbAs("admin@example.com", big.ID); w.Code != 404 {
		t.Fatalf("non-member thumb: %d", w.Code)
	}
	// Deleting the photo removes its thumbnail too.
	mustCall(t, tools, owner, "delete_attachment", AttachmentInput{p.ID, big.ID})
	if err = s.CleanupAttachments(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.files[thumbnailKey(p.ID, big.ID)]; ok {
		t.Fatal("thumbnail left after delete")
	}
	if _, ok := m.files[attachmentKey(p.ID, big.ID)]; ok {
		t.Fatal("original left after delete")
	}

	// Ordinary project files are not served inline.
	plain := preparedAttachment(t, tools, owner, p.ID, png)
	if _, err = s.UploadAttachment(ctx, owner, AttachmentInput{p.ID, plain.ID}, strings.NewReader(png)); err != nil {
		t.Fatal(err)
	}
	if w := get("viewer@example.com", p.ID, plain.ID); w.Code != 404 {
		t.Fatalf("document file served inline: %d", w.Code)
	}

	mustCall(t, tools, owner, "delete_attachment", AttachmentInput{p.ID, img.ID})
	full = mustCall(t, tools, viewer, "get_issue", IssueRef{ProjectID: p.ID, IssueID: issue.ID}).(Issue)
	if full.AttachmentCount != 0 || len(full.Attachments) != 0 {
		t.Fatal("deleted image still listed")
	}
	if w := get("viewer@example.com", p.ID, img.ID); w.Code != 404 {
		t.Fatalf("deleted image served: %d", w.Code)
	}
}

func TestIssueAnswerAndCommentImages(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	s.Objects = &memoryObjects{files: map[string][]byte{}}
	tools := NewTools(s)
	admin := loginAs(t, s, "admin@example.com")
	asker := inviteAs(t, s, admin, "asker@example.com")
	owner := inviteAs(t, s, admin, "owner@example.com")
	p := mustCall(t, tools, asker, "create_project", CreateProjectInput{Slug: "parts", Name: "Parts"}).(Project)
	mustCall(t, tools, asker, "set_project_member", MemberInput{ProjectID: p.ID, UserID: owner.User.ID, Role: "editor"})
	issue := mustCall(t, tools, asker, "create_issue", CreateIssueInput{ProjectID: p.ID, Title: "배포 오류", Body: "로그 확인 부탁"}).(Issue)
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x01", 30)
	upload := func(who Identity, in PrepareAttachmentInput) (Attachment, error) {
		in.ProjectID, in.IssueID, in.Filename, in.SizeBytes = p.ID, issue.ID, "shot.png", int64(len(png))
		out, err := call(t, tools, who, "prepare_attachment_upload", in)
		if err != nil {
			return Attachment{}, err
		}
		f := out.(map[string]any)["attachment"].(Attachment)
		stored, err := s.UploadAttachment(ctx, who, AttachmentInput{p.ID, f.ID}, strings.NewReader(png))
		if err != nil {
			t.Fatal(err)
		}
		return stored.(Attachment), nil
	}

	// No answer yet; bad part names and stray comment IDs are rejected.
	_, err := upload(owner, PrepareAttachmentInput{IssuePart: "answer"})
	denied(t, err, "CONFLICT")
	_, err = upload(owner, PrepareAttachmentInput{IssuePart: "title"})
	denied(t, err, "INVALID_INPUT")
	_, err = upload(owner, PrepareAttachmentInput{IssuePart: "comment"})
	denied(t, err, "INVALID_INPUT")
	_, err = call(t, tools, owner, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: p.ID, IssuePart: "body", Filename: "a.png", SizeBytes: 10})
	denied(t, err, "INVALID_INPUT")

	mustCall(t, tools, owner, "answer_issue", AnswerIssueInput{ProjectID: p.ID, IssueID: issue.ID, Answer: "첫 답변"})
	_, err = upload(asker, PrepareAttachmentInput{IssuePart: "answer"})
	denied(t, err, "FORBIDDEN")
	answerImg, err := upload(owner, PrepareAttachmentInput{IssuePart: "answer"})
	if err != nil {
		t.Fatal(err)
	}
	commented := mustCall(t, tools, asker, "comment_issue", CommentIssueInput{ProjectID: p.ID, IssueID: issue.ID, Body: "추가 로그"}).(Issue)
	if commented.CreatedCommentID == "" {
		t.Fatal("comment_issue did not return created_comment_id")
	}
	_, err = upload(owner, PrepareAttachmentInput{IssuePart: "comment", IssueCommentID: commented.CreatedCommentID})
	denied(t, err, "FORBIDDEN")
	commentImg, err := upload(asker, PrepareAttachmentInput{IssuePart: "comment", IssueCommentID: commented.CreatedCommentID})
	if err != nil {
		t.Fatal(err)
	}
	bodyImg, err := upload(owner, PrepareAttachmentInput{})
	if err != nil {
		t.Fatal(err)
	}

	full := mustCall(t, tools, asker, "get_issue", IssueRef{ProjectID: p.ID, IssueID: issue.ID}).(Issue)
	if full.AttachmentCount != 3 || len(full.Attachments) != 1 || full.Attachments[0].ID != bodyImg.ID ||
		len(full.AnswerAttachments) != 1 || full.AnswerAttachments[0].ID != answerImg.ID {
		t.Fatalf("parts not split: body %+v answer %+v", full.Attachments, full.AnswerAttachments)
	}
	if len(full.Comments) != 1 || len(full.Comments[0].Attachments) != 1 || full.Comments[0].Attachments[0].ID != commentImg.ID {
		t.Fatalf("comment images: %+v", full.Comments)
	}

	// Replacing the answer moves its images to the comment that keeps the old text.
	mustCall(t, tools, owner, "answer_issue", AnswerIssueInput{ProjectID: p.ID, IssueID: issue.ID, Answer: "수정한 답변"})
	full = mustCall(t, tools, asker, "get_issue", IssueRef{ProjectID: p.ID, IssueID: issue.ID}).(Issue)
	if len(full.AnswerAttachments) != 0 {
		t.Fatal("old answer images stayed on the new answer")
	}
	moved := false
	for _, c := range full.Comments {
		if strings.HasPrefix(c.Body, "[이전 답변]") && len(c.Attachments) == 1 && c.Attachments[0].ID == answerImg.ID {
			moved = true
		}
	}
	if !moved {
		t.Fatalf("answer image did not follow the previous answer: %+v", full.Comments)
	}
}
