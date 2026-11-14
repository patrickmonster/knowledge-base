package kb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
)

type fakeScanner struct {
	signature string
	err       error
	scanned   []byte
}

func (f *fakeScanner) Name() string { return "fake" }
func (f *fakeScanner) Scan(_ context.Context, r io.Reader) (string, error) {
	f.scanned, _ = io.ReadAll(r)
	return f.signature, f.err
}

func TestAttachmentPolicyParsersAndDetectors(t *testing.T) {
	if q, err := ParseAttachmentQuota(""); err != nil || q != DefaultAttachmentQuotaBytes {
		t.Fatalf("default quota: %d %v", q, err)
	}
	if q, err := ParseAttachmentQuota("0"); err != nil || q != 0 {
		t.Fatalf("unlimited quota: %d %v", q, err)
	}
	if q, err := ParseAttachmentQuota("100"); err != nil || q != 100<<20 {
		t.Fatalf("quota MiB: %d %v", q, err)
	}
	for _, bad := range []string{"-1", "10", "x", "2000000"} {
		if _, err := ParseAttachmentQuota(bad); err == nil {
			t.Fatalf("quota %q accepted", bad)
		}
	}
	for _, name := range []string{"setup.exe", "RUN.BAT", "x.ps1", "evil.exe.", "a.lnk"} {
		if !blockedFilename(name) {
			t.Fatalf("%s should be blocked", name)
		}
	}
	for _, name := range []string{"plan.xlsx", "report.pdf", "notes.md", "exe", "script.py"} {
		if blockedFilename(name) {
			t.Fatalf("%s should be allowed", name)
		}
	}
	pe := make([]byte, 0x100)
	copy(pe, "MZ")
	binary.LittleEndian.PutUint32(pe[0x3c:], 0x80)
	copy(pe[0x80:], "PE\x00\x00")
	if !executableMagic(pe) || !executableMagic([]byte("\x7fELF\x02\x01")) || !executableMagic([]byte{0xcf, 0xfa, 0xed, 0xfe, 7}) {
		t.Fatal("executables not detected")
	}
	if executableMagic([]byte("MZ 프로젝트 메모로 시작하는 평범한 텍스트입니다. 실행 파일이 아닙니다. ......")) || executableMagic([]byte("%PDF-1.7")) {
		t.Fatal("plain files flagged as executables")
	}
	for reply, want := range map[string]string{"stream: OK": "", "stream: Eicar-Test-Signature FOUND": "Eicar-Test-Signature"} {
		if got, err := parseClamReply(reply); err != nil || got != want {
			t.Fatalf("%q: %q %v", reply, got, err)
		}
	}
	for _, reply := range []string{"INSTREAM size limit exceeded. ERROR", "stream: lstat() failed ERROR", ""} {
		if _, err := parseClamReply(reply); err == nil {
			t.Fatalf("%q should fail closed", reply)
		}
	}
}

// TestClamAVInstream checks the clamd wire protocol against a fake daemon.
func TestClamAVInstream(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	received := make(chan []byte, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		cmd, _ := r.ReadString(0)
		if cmd != "zINSTREAM\x00" {
			conn.Write([]byte("UNKNOWN COMMAND\x00"))
			return
		}
		var body bytes.Buffer
		for {
			var n uint32
			if binary.Read(r, binary.BigEndian, &n) != nil {
				return
			}
			if n == 0 {
				break
			}
			io.CopyN(&body, r, int64(n))
		}
		received <- body.Bytes()
		if strings.Contains(body.String(), "EICAR") {
			conn.Write([]byte("stream: Eicar-Test-Signature FOUND\x00"))
		} else {
			conn.Write([]byte("stream: OK\x00"))
		}
	}()
	payload := strings.Repeat("a", 70<<10) + "EICAR"
	sig, err := ClamAV{Address: ln.Addr().String()}.Scan(context.Background(), strings.NewReader(payload))
	if err != nil || sig != "Eicar-Test-Signature" {
		t.Fatalf("scan: %q %v", sig, err)
	}
	if got := <-received; string(got) != payload {
		t.Fatalf("daemon received %d bytes, want %d", len(got), len(payload))
	}
	ln.Close()
	if _, err := (ClamAV{Address: ln.Addr().String()}).Scan(context.Background(), strings.NewReader("x")); err == nil {
		t.Fatal("unreachable clamd must fail closed")
	}
}

func TestAttachmentQuotaAndScreening(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	m := &memoryObjects{files: map[string][]byte{}}
	s.Objects = m
	s.AttachmentQuotaBytes = 100
	tools := NewTools(s)
	owner := loginAs(t, s, "admin@example.com")
	p := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "quota", Name: "quota"}).(Project)
	other := mustCall(t, tools, owner, "create_project", CreateProjectInput{Slug: "quota-other", Name: "quota other"}).(Project)
	prepare := func(pid, name string, size int64) (Attachment, error) {
		out, err := call(t, tools, owner, "prepare_attachment_upload", PrepareAttachmentInput{ProjectID: pid, Filename: name, SizeBytes: size})
		if err != nil {
			return Attachment{}, err
		}
		return out.(map[string]any)["attachment"].(Attachment), nil
	}
	upload := func(f Attachment, body string) error {
		_, err := s.UploadAttachment(ctx, owner, AttachmentInput{ProjectID: f.ProjectID, AttachmentID: f.ID}, strings.NewReader(body))
		return err
	}

	// Executable names are refused at reservation time.
	_, err := prepare(p.ID, "setup.exe", 10)
	denied(t, err, "BLOCKED_FILE")

	// Reservations count toward the quota, per project.
	first, err := prepare(p.ID, "a.txt", 60)
	if err != nil {
		t.Fatal(err)
	}
	_, err = prepare(p.ID, "b.txt", 41)
	denied(t, err, "QUOTA_EXCEEDED")
	if _, err = prepare(other.ID, "b.txt", 41); err != nil {
		t.Fatalf("quota must be per project: %v", err)
	}
	if err = upload(first, strings.Repeat("a", 60)); err != nil {
		t.Fatal(err)
	}
	usage := mustCall(t, tools, owner, "get_attachment_usage", ProjectInput{ProjectID: p.ID}).(map[string]any)
	if usage["used_bytes"] != int64(60) || usage["quota_bytes"] != int64(100) || usage["malware_scanner"] != "none" || usage["executables_blocked"] != true {
		t.Fatalf("usage: %v", usage)
	}

	// An executable renamed to a document is caught by its header and frees its reservation.
	pe := make([]byte, 0x100)
	copy(pe, "MZ")
	binary.LittleEndian.PutUint32(pe[0x3c:], 0x80)
	copy(pe[0x80:], "PE\x00\x00")
	disguised, err := prepare(p.ID, "report.pdf", int64(len(pe)))
	denied(t, err, "QUOTA_EXCEEDED") // 60 + 256 > 100
	s.AttachmentQuotaBytes = 1000
	if disguised, err = prepare(p.ID, "report.pdf", int64(len(pe))); err != nil {
		t.Fatal(err)
	}
	denied(t, upload(disguised, string(pe)), "BLOCKED_FILE")
	if len(m.files) != 1 {
		t.Fatalf("blocked file reached the bucket: %d objects", len(m.files))
	}

	// Malware scanner: infected files are rejected and audited with the signature only.
	scanner := &fakeScanner{signature: "Eicar-Test-Signature"}
	s.Scanner = scanner
	infected, err := prepare(p.ID, "notes.txt", 5)
	if err != nil {
		t.Fatal(err)
	}
	denied(t, upload(infected, "EICAR"), "MALWARE_DETECTED")
	if string(scanner.scanned) != "EICAR" || len(m.files) != 1 {
		t.Fatalf("scanner input %q, objects %d", scanner.scanned, len(m.files))
	}
	var status, details string
	if err = s.DB.QueryRow(`SELECT status FROM attachments WHERE id=$1`, infected.ID).Scan(&status); err != nil || status != "deleting" {
		t.Fatalf("infected reservation should be queued for deletion: %q %v", status, err)
	}
	if err = s.DB.QueryRow(`SELECT details::text FROM audit_events WHERE action='attachment_rejected' AND target_id=$1`, infected.ID).Scan(&details); err != nil ||
		!strings.Contains(details, "Eicar-Test-Signature") || strings.Contains(details, "notes.txt") {
		t.Fatalf("rejection audit: %q %v", details, err)
	}

	// Scanner outage fails closed.
	scanner.signature, scanner.err = "", errors.New("down")
	outage, err := prepare(p.ID, "notes.txt", 5)
	if err != nil {
		t.Fatal(err)
	}
	denied(t, upload(outage, "hello"), "SCAN_UNAVAILABLE")

	// Clean files pass the scanner and are stored.
	scanner.err = nil
	clean, err := prepare(p.ID, "notes.txt", 5)
	if err != nil {
		t.Fatal(err)
	}
	if err = upload(clean, "hello"); err != nil {
		t.Fatal(err)
	}
	if len(m.files) != 2 {
		t.Fatalf("clean file not stored: %d objects", len(m.files))
	}
}
