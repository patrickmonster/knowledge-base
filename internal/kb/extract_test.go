package kb

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// TestMain lets ProcessExtractor tests run this test binary as the
// "extract-text" child, exactly as the server runs its own executable.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "extract-text" {
		if err := RunExtractTextCommand(os.Args[2:], os.Stdout); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func writeZip(t *testing.T, name string, parts map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for n, body := range parts {
		f, err := w.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(file, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

const wordNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

func docxWith(t *testing.T, body string) string {
	return writeZip(t, "doc.docx", map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8"?><Types/>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document ` + wordNS + `><w:body>` + body + `</w:body></w:document>`,
	})
}

func TestExtractDOCX(t *testing.T) {
	file := docxWith(t, `<w:p><w:r><w:t>환불 정책</w:t></w:r></w:p>`+
		`<w:p><w:r><w:t xml:space="preserve">결제 후 </w:t></w:r><w:r><w:t>7일</w:t><w:tab/><w:t>이내</w:t></w:r><w:del><w:r><w:delText>삭제된 문구</w:delText></w:r></w:del></w:p>`+
		`<w:tbl><w:tr><w:tc><w:p><w:r><w:t>항목</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>금액|원</w:t></w:r></w:p></w:tc></w:tr>`+
		`<w:tr><w:tc><w:p><w:r><w:t>기본</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>10000</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`+
		`<w:p><w:r><w:t>끝 &amp; 요약</w:t></w:r></w:p>`)
	res, err := ExtractText("docx", file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"환불 정책", "결제 후 7일\t이내", "| 항목 | 금액\\|원 |\n| --- | --- |\n| 기본 | 10000 |", "끝 & 요약"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("missing %q in:\n%s", want, res.Text)
		}
	}
	if strings.Contains(res.Text, "삭제된") || res.Truncated {
		t.Fatalf("deleted text or truncation: %+v", res)
	}
}

func TestExtractXLSX(t *testing.T) {
	file := writeZip(t, "book.xlsx", map[string]string{
		"xl/workbook.xml": `<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>` +
			`<sheet name="매출" sheetId="1" r:id="rId1"/><sheet name="메모" sheetId="2" r:id="rId2"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="worksheet" Target="/xl/worksheets/other.xml"/></Relationships>`,
		"xl/sharedStrings.xml": `<?xml version="1.0" encoding="UTF-8"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
			`<si><t>월</t></si><si><t>매출액</t></si><si><r><t>1</t></r><r><t>월</t></r><rPh><t>いち</t></rPh></si></sst>`,
		"xl/worksheets/sheet1.xml": `<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
			`<row r="1"><c r="A1" t="s"><v>0</v></c><c r="C1" t="s"><v>1</v></c></row>` +
			`<row r="2"><c r="A2" t="s"><v>2</v></c><c r="B2" t="b"><v>1</v></c><c r="C2"><f>SUM(X1)</f><v>1250000</v></c></row>` +
			`<row r="3"><c r="A3" t="s"><v>99</v></c></row></sheetData></worksheet>`,
		"xl/worksheets/other.xml": `<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` +
			`<row r="1"><c r="B1" t="inlineStr"><is><t>인라인 메모</t></is></c></row></sheetData></worksheet>`,
	})
	res, err := ExtractText("xlsx", file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## 시트: 매출", "| 월 |  | 매출액 |", "| --- | --- | --- |", "| 1월 | TRUE | 1250000 |", "## 시트: 메모", "|  | 인라인 메모 |"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("missing %q in:\n%s", want, res.Text)
		}
	}
	if strings.Contains(res.Text, "いち") || strings.Contains(res.Text, "SUM") || res.Units != 2 {
		t.Fatalf("phonetic text, formula or sheet count wrong: %+v", res)
	}
}

// buildPDF writes a minimal PDF whose objects are given in order (object 1 is
// the catalog), computing the xref table.
func buildPDF(t *testing.T, objects []string) string {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects))
	for i, o := range objects {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	file := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(file, b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func stream(s string) string {
	return fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(s), s)
}

func TestExtractPDF(t *testing.T) {
	toUnicode := "/CIDInit /ProcSet findresource begin 12 dict begin begincmap /CMapName /T def 1 begincodespacerange <0000> <FFFF> endcodespacerange\n" +
		"3 beginbfchar <0001> <D658> <0002> <BD88> <0003> <0020> endbfchar endcmap CMapName currentdict /CMap defineresource pop end end"
	file := buildPDF(t, []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R 4 0 R 9 0 R] /Count 3 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 6 0 R >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F2 7 0 R >> >> /Contents 8 0 R >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
		stream("BT /F1 12 Tf 72 720 Td (Refund policy) Tj 0 -20 Td (Within 7 days) Tj ET"),
		"<< /Type /Font /Subtype /Type0 /BaseFont /KR /Encoding /Identity-H /DescendantFonts [<< /Type /Font /Subtype /CIDFontType2 /BaseFont /KR /CIDSystemInfo << /Registry (Adobe) /Ordering (Identity) /Supplement 0 >> >>] /ToUnicode 10 0 R >>",
		stream("BT /F2 12 Tf 72 720 Td <000100020003> Tj ET"),
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 11 0 R >>",
		stream(toUnicode),
		stream(""),
	})
	res, err := ExtractText("pdf", file)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## p.1", "Refund policy", "Within 7 days", "## p.2", "환불"} {
		if !strings.Contains(res.Text, want) {
			t.Fatalf("missing %q in:\n%s", want, res.Text)
		}
	}
	if strings.Contains(res.Text, "## p.3") || res.Units != 3 {
		t.Fatalf("empty page listed or page count wrong: %+v", res)
	}
}

func TestExtractRejectsBadAndHostileFiles(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk")
	if err := os.WriteFile(junk, []byte("%PDF-1.7\nnot really"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"pdf", "docx", "xlsx", "exe"} {
		if _, err := ExtractText(format, junk); ExtractCode(err) != ExtractInvalidFile {
			t.Fatalf("%s junk: %v", format, err)
		}
	}
	// A small archive that expands past the per-part limit.
	bomb := docxWith(t, `<w:p><w:r><w:t>`+strings.Repeat("A", maxZipEntryBytes+1)+`</w:t></w:r></w:p>`)
	if info, _ := os.Stat(bomb); info.Size() > 1<<20 {
		t.Fatalf("bomb fixture is not compressed: %d", info.Size())
	}
	if _, err := ExtractText("docx", bomb); ExtractCode(err) != ExtractTooLarge {
		t.Fatalf("bomb: %v", err)
	}
	encrypted := writeZip(t, "secret.docx", map[string]string{"EncryptedPackage": "x"})
	if _, err := ExtractText("docx", encrypted); ExtractCode(err) != ExtractEncrypted {
		t.Fatalf("encrypted: %v", err)
	}
	// Unknown entities are not expanded and external entities are never fetched.
	xxe := docxWith(t, `<w:p><w:r><w:t>&xxe;안전</w:t></w:r></w:p>`)
	res, err := ExtractText("docx", xxe)
	if err != nil || !strings.Contains(res.Text, "안전") {
		t.Fatalf("entity handling: %v %+v", err, res)
	}
}

func TestExtractTruncatesLongText(t *testing.T) {
	file := docxWith(t, strings.Repeat(`<w:p><w:r><w:t>가나다라마바사아자차 </w:t></w:r></w:p>`, MaxExtractedRunes/5))
	res, err := ExtractText("docx", file)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Truncated || utf8.RuneCountInString(res.Text) > MaxExtractedRunes || !utf8.ValidString(res.Text) {
		t.Fatalf("bad truncation: truncated=%v runes=%d", res.Truncated, utf8.RuneCountInString(res.Text))
	}
}

func TestExtractFormat(t *testing.T) {
	for name, want := range map[string]string{"a.PDF": "pdf", "보고서.docx": "docx", "표.XLSX": "xlsx", "old.doc": "", "old.xls": "", "notes.txt": "", "pdf": ""} {
		if got := ExtractFormat(name); got != want {
			t.Fatalf("%s: got %q want %q", name, got, want)
		}
	}
}

func TestProcessExtractorRunsChild(t *testing.T) {
	ctx := context.Background()
	p := ProcessExtractor{Executable: os.Args[0], Timeout: 30 * time.Second}
	res, err := p.Extract(ctx, "docx", docxWith(t, `<w:p><w:r><w:t>자식 프로세스</w:t></w:r></w:p>`))
	if err != nil || res.Text != "자식 프로세스" {
		t.Fatalf("child extract: %v %+v", err, res)
	}
	junk := filepath.Join(t.TempDir(), "junk.pdf")
	if err := os.WriteFile(junk, []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Extract(ctx, "pdf", junk); ExtractCode(err) != ExtractInvalidFile {
		t.Fatalf("child junk: %v", err)
	}
	missing := ProcessExtractor{Executable: filepath.Join(t.TempDir(), "missing")}
	if _, err := missing.Extract(ctx, "pdf", junk); ExtractCode(err) != ExtractFailed {
		t.Fatalf("missing executable: %v", err)
	}
	expired, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Extract(expired, "pdf", junk); ExtractCode(err) != ExtractTimeout {
		t.Fatalf("cancelled: %v", err)
	}
}
