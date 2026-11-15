package kb

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// Attachment text extraction turns uploaded PDF, DOCX and XLSX files into plain
// text that is stored as an artifact document, so the existing chunking,
// embedding and search paths index file contents. Uploaded files are untrusted:
// every reader below caps its input, output and archive expansion, and the
// server runs extraction in a child process (ProcessExtractor) so a crash, hang
// or memory blow-up in a parser cannot take the server down.

const (
	// MaxExtractedRunes keeps the extracted text plus the document header under
	// the 100000 character document limit.
	MaxExtractedRunes = 95000
	// maxZipEntryBytes caps the decompressed size of one DOCX/XLSX part.
	maxZipEntryBytes = 64 << 20
	// maxZipTotalBytes caps the decompressed bytes read from one archive.
	maxZipTotalBytes = 160 << 20
	maxZipEntries    = 20000
	maxPDFPages      = 3000
	maxSheetColumns  = 200
	maxSharedStrings = 1 << 20
)

// Extraction error codes stored in attachments.text_error.
const (
	ExtractInvalidFile = "INVALID_FILE"
	ExtractEncrypted   = "ENCRYPTED"
	ExtractTooLarge    = "TOO_LARGE"
	ExtractTimeout     = "TIMEOUT"
	ExtractFailed      = "EXTRACT_FAILED"
)

// ExtractError carries a stable code safe to store and show; file content and
// parser messages are never stored.
type ExtractError struct{ Code string }

func (e *ExtractError) Error() string { return "text extraction failed: " + e.Code }

func extractErr(code string) error { return &ExtractError{Code: code} }

// ExtractCode returns the stable code of an extraction error.
func ExtractCode(err error) string {
	var e *ExtractError
	if errors.As(err, &e) {
		return e.Code
	}
	return ExtractFailed
}

// ExtractResult is the text taken from one file.
type ExtractResult struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
	// Units counts pages (PDF) or sheets (XLSX); 0 for DOCX.
	Units int `json:"units"`
}

// ExtractFormat maps a filename to a supported format, or "" when the file is
// not extracted.
func ExtractFormat(filename string) string {
	switch strings.ToLower(path.Ext(filename)) {
	case ".pdf":
		return "pdf"
	case ".docx":
		return "docx"
	case ".xlsx":
		return "xlsx"
	}
	return ""
}

// ExtractText reads a file in-process. Parsers may panic on malformed input;
// panics become INVALID_FILE.
func ExtractText(format, file string) (res ExtractResult, err error) {
	defer func() {
		if r := recover(); r != nil {
			res, err = ExtractResult{}, extractErr(ExtractInvalidFile)
		}
	}()
	switch format {
	case "pdf":
		return extractPDF(file)
	case "docx":
		return extractDOCX(file)
	case "xlsx":
		return extractXLSX(file)
	}
	return ExtractResult{}, extractErr(ExtractInvalidFile)
}

// textBuilder collects output up to MaxExtractedRunes and normalizes whitespace.
type textBuilder struct {
	b         strings.Builder
	runes     int
	truncated bool
}

func (t *textBuilder) full() bool { return t.truncated }

func (t *textBuilder) write(s string) {
	if t.truncated || s == "" {
		return
	}
	s = strings.ToValidUTF8(s, "")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || r == '�' {
			return -1
		}
		return r
	}, s)
	n := utf8.RuneCountInString(s)
	if room := MaxExtractedRunes - t.runes; n > room {
		end := len(s)
		for i := range s {
			if room == 0 {
				end = i
				break
			}
			room--
		}
		t.b.WriteString(s[:end])
		t.runes = MaxExtractedRunes
		t.truncated = true
		return
	}
	t.b.WriteString(s)
	t.runes += n
}

func (t *textBuilder) result(units int) ExtractResult {
	return ExtractResult{Text: tidyText(t.b.String()), Truncated: t.truncated, Units: units}
}

// tidyText trims trailing spaces and collapses runs of blank lines.
func tidyText(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := 0
	for _, line := range lines {
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		if strings.TrimSpace(line) == "" {
			blank++
			if blank > 1 {
				continue
			}
			line = ""
		} else {
			blank = 0
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

func extractPDF(file string) (ExtractResult, error) {
	f, err := os.Open(file)
	if err != nil {
		return ExtractResult{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return ExtractResult{}, err
	}
	r, err := pdf.NewReader(f, info.Size())
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "encrypt") || strings.Contains(strings.ToLower(err.Error()), "password") {
			return ExtractResult{}, extractErr(ExtractEncrypted)
		}
		return ExtractResult{}, extractErr(ExtractInvalidFile)
	}
	pages := r.NumPage()
	if pages < 0 || pages > maxPDFPages {
		return ExtractResult{}, extractErr(ExtractTooLarge)
	}
	var t textBuilder
	fonts := map[string]*pdf.Font{}
	for i := 1; i <= pages && !t.full(); i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		for _, name := range p.Fonts() {
			if _, ok := fonts[name]; !ok {
				font := p.Font(name)
				fonts[name] = &font
			}
		}
		text, err := pageText(p, fonts)
		if err != nil {
			continue // a broken page should not hide the rest of the document
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		t.write(fmt.Sprintf("\n\n## p.%d\n\n", i))
		t.write(text)
	}
	return t.result(pages), nil
}

// pageText keeps PDF line breaks by grouping text into rows. Rows come from the
// page's full text state first; the library's GetTextByRow only follows Tm and
// puts every line moved with Td, TD, T* or ' on one row, so it is a fallback,
// followed by the library's plain text.
func pageText(p pdf.Page, fonts map[string]*pdf.Font) (string, error) {
	if text := positionedPageText(p); text != "" {
		return text, nil
	}
	rows, err := p.GetTextByRow()
	if err == nil && len(rows) > 0 {
		var b strings.Builder
		for _, row := range rows {
			words := make([]string, 0, len(row.Content))
			for _, w := range row.Content {
				words = append(words, w.S)
			}
			b.WriteString(strings.Join(words, ""))
			b.WriteByte('\n')
		}
		return b.String(), nil
	}
	return p.GetPlainText(fonts)
}

// positionedPageText rebuilds lines from glyph positions computed with the
// whole text state (Tm, Td, TD, T*, TL, ', ", CTM).
//   - Glyphs whose baselines round to within a point share a line; lines run
//     top to bottom.
//   - Inside a line, glyphs keep content-stream order, so negative kerning or
//     fonts without width tables (every glyph at the same x) cannot shuffle
//     letters. Only runs that jump back left by more than a font size, i.e.
//     text placed separately, are reordered left to right.
//   - A gap wider than a fifth of the font size after a glyph of known width
//     becomes a space; control characters some fonts map glyphs to are dropped.
//
// It returns "" when the page cannot be interpreted so the caller falls back to
// the library's readers.
func positionedPageText(p pdf.Page) (text string) {
	defer func() {
		if recover() != nil {
			text = ""
		}
	}()
	type line struct {
		y      float64
		glyphs []pdf.Text
	}
	var lines []*line
	byY := map[int]*line{}
	for _, g := range p.Content().Text {
		g.S = strings.Map(func(r rune) rune {
			switch {
			case r == '\t':
				return ' '
			case r < 0x20 || r == 0x7f:
				return -1
			}
			return r
		}, g.S)
		if g.S == "" {
			continue
		}
		key := int(math.Round(g.Y))
		l := byY[key]
		if l == nil {
			l = byY[key-1]
		}
		if l == nil {
			l = byY[key+1]
		}
		if l == nil {
			l = &line{y: g.Y}
			lines = append(lines, l)
		}
		byY[key] = l
		l.glyphs = append(l.glyphs, g)
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].y > lines[j].y })
	var b strings.Builder
	for _, l := range lines {
		var runs [][]pdf.Text
		for i, g := range l.glyphs {
			if i == 0 || g.X < l.glyphs[i-1].X-math.Max(g.FontSize, 1) {
				runs = append(runs, nil)
			}
			runs[len(runs)-1] = append(runs[len(runs)-1], g)
		}
		sort.SliceStable(runs, func(i, j int) bool { return runs[i][0].X < runs[j][0].X })
		var prev *pdf.Text
		var segX float64 // start of the current run of zero-width glyphs sharing one x
		segN := 0
		for _, run := range runs {
			for i := range run {
				g := &run[i]
				if prev != nil && !strings.HasSuffix(prev.S, " ") && !strings.HasPrefix(g.S, " ") {
					end := prev.X + prev.W
					if prev.W == 0 {
						// No width table (common for CID fonts): every glyph of a
						// show-text call sits at one x. Assume at most 1 em per glyph
						// so only a clear jump past the segment counts as a gap.
						end = segX + float64(segN)*math.Max(prev.FontSize, 1)
					}
					if g.X-end > 0.2*math.Max(g.FontSize, 1) {
						b.WriteByte(' ')
					}
				}
				b.WriteString(g.S)
				if g.W > 0 {
					segN = 0
				} else {
					if segN == 0 || g.X != segX {
						segX, segN = g.X, 0
					}
					segN += utf8.RuneCountInString(g.S)
				}
				prev = g
			}
		}
		b.WriteByte('\n')
	}
	if strings.TrimSpace(b.String()) == "" {
		return ""
	}
	return b.String()
}

// zipArchive guards DOCX/XLSX reads against archive bombs.
type zipArchive struct {
	r     *zip.ReadCloser
	files map[string]*zip.File
	read  int64
}

func openZip(file string) (*zipArchive, error) {
	r, err := zip.OpenReader(file)
	if err != nil {
		return nil, extractErr(ExtractInvalidFile)
	}
	if len(r.File) > maxZipEntries {
		r.Close()
		return nil, extractErr(ExtractTooLarge)
	}
	z := &zipArchive{r: r, files: map[string]*zip.File{}}
	for _, f := range r.File {
		z.files[strings.TrimPrefix(f.Name, "/")] = f
	}
	return z, nil
}

func (z *zipArchive) Close() error { return z.r.Close() }

// open returns a reader capped by the per-entry and per-archive limits, whatever
// sizes the archive headers claim. ok is false when the part does not exist.
func (z *zipArchive) open(name string) (io.ReadCloser, bool, error) {
	f, ok := z.files[name]
	if !ok {
		return nil, false, nil
	}
	if f.UncompressedSize64 > maxZipEntryBytes {
		return nil, true, extractErr(ExtractTooLarge)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, true, extractErr(ExtractInvalidFile)
	}
	return &cappedReader{rc: rc, z: z, left: maxZipEntryBytes}, true, nil
}

type cappedReader struct {
	rc   io.ReadCloser
	z    *zipArchive
	left int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.left <= 0 || c.z.read >= maxZipTotalBytes {
		return 0, extractErr(ExtractTooLarge)
	}
	if int64(len(p)) > c.left {
		p = p[:c.left]
	}
	n, err := c.rc.Read(p)
	c.left -= int64(n)
	c.z.read += int64(n)
	return n, err
}
func (c *cappedReader) Close() error { return c.rc.Close() }

// xmlDecoder reads untrusted XML. encoding/xml does not expand custom entities
// or fetch external resources, so entity bombs and XXE do not apply.
func xmlDecoder(r io.Reader) *xml.Decoder {
	d := xml.NewDecoder(r)
	d.Strict = false
	d.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
		if strings.EqualFold(label, "utf-8") || strings.EqualFold(label, "utf8") {
			return input, nil
		}
		return nil, extractErr(ExtractInvalidFile)
	}
	return d
}

func asExtractErr(err error) error {
	var e *ExtractError
	if errors.As(err, &e) {
		return e
	}
	return extractErr(ExtractInvalidFile)
}

func extractDOCX(file string) (ExtractResult, error) {
	z, err := openZip(file)
	if err != nil {
		return ExtractResult{}, err
	}
	defer z.Close()
	if _, ok := z.files["EncryptedPackage"]; ok {
		return ExtractResult{}, extractErr(ExtractEncrypted)
	}
	rc, ok, err := z.open("word/document.xml")
	if err != nil {
		return ExtractResult{}, err
	}
	if !ok {
		return ExtractResult{}, extractErr(ExtractInvalidFile)
	}
	defer rc.Close()
	var t textBuilder
	d := xmlDecoder(rc)
	inText, inDeleted := false, 0
	var cells []string
	var cell strings.Builder
	tableDepth, tableRows := 0, 0
	emit := func(s string) {
		if tableDepth > 0 {
			cell.WriteString(s)
		} else {
			t.write(s)
		}
	}
	for !t.full() {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ExtractResult{}, asExtractErr(err)
		}
		switch e := tok.(type) {
		case xml.StartElement:
			switch e.Name.Local {
			case "t":
				inText = inDeleted == 0
			case "delText":
				inText = false
			case "del":
				inDeleted++
			case "tab":
				emit("\t")
			case "br", "cr":
				emit("\n")
			case "tbl":
				tableDepth++
				if tableDepth == 1 {
					tableRows = 0
					t.write("\n")
				}
			case "tr":
				if tableDepth == 1 {
					cells = cells[:0]
				}
			case "tc":
				if tableDepth == 1 {
					cell.Reset()
				}
			}
		case xml.EndElement:
			switch e.Name.Local {
			case "t":
				inText = false
			case "del":
				if inDeleted > 0 {
					inDeleted--
				}
			case "p":
				emit("\n")
			case "tc":
				if tableDepth == 1 {
					cells = append(cells, strings.Join(strings.Fields(cell.String()), " "))
				}
			case "tr":
				if tableDepth == 1 {
					t.write("| " + strings.Join(escapeCells(cells), " | ") + " |\n")
					if tableRows == 0 {
						t.write(tableSeparator(len(cells)))
					}
					tableRows++
				}
			case "tbl":
				tableDepth--
				if tableDepth == 0 {
					t.write("\n")
				}
			}
		case xml.CharData:
			if inText {
				emit(string(e))
			}
		}
	}
	return t.result(0), nil
}

func tableSeparator(n int) string {
	if n < 1 {
		n = 1
	}
	return "|" + strings.Repeat(" --- |", n) + "\n"
}

func escapeCells(cells []string) []string {
	out := make([]string, len(cells))
	for i, c := range cells {
		out[i] = strings.ReplaceAll(c, "|", "\\|")
	}
	return out
}

type xlsxSheet struct {
	name   string
	target string
}

func extractXLSX(file string) (ExtractResult, error) {
	z, err := openZip(file)
	if err != nil {
		return ExtractResult{}, err
	}
	defer z.Close()
	if _, ok := z.files["EncryptedPackage"]; ok {
		return ExtractResult{}, extractErr(ExtractEncrypted)
	}
	sheets, err := xlsxSheets(z)
	if err != nil {
		return ExtractResult{}, err
	}
	shared, err := xlsxSharedStrings(z)
	if err != nil {
		return ExtractResult{}, err
	}
	var t textBuilder
	for _, sh := range sheets {
		if t.full() {
			break
		}
		t.write("\n\n## 시트: " + sh.name + "\n\n")
		if err := xlsxSheetText(z, sh.target, shared, &t); err != nil {
			return ExtractResult{}, err
		}
	}
	return t.result(len(sheets)), nil
}

func xlsxSheets(z *zipArchive) ([]xlsxSheet, error) {
	rels := map[string]string{}
	if rc, ok, err := z.open("xl/_rels/workbook.xml.rels"); err != nil {
		return nil, err
	} else if ok {
		d := xmlDecoder(rc)
		for {
			tok, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				rc.Close()
				return nil, asExtractErr(err)
			}
			if e, ok := tok.(xml.StartElement); ok && e.Name.Local == "Relationship" {
				var id, target string
				for _, a := range e.Attr {
					switch a.Name.Local {
					case "Id":
						id = a.Value
					case "Target":
						target = a.Value
					}
				}
				if strings.HasPrefix(target, "/") {
					target = strings.TrimPrefix(target, "/")
				} else {
					target = path.Clean("xl/" + target)
				}
				rels[id] = target
			}
		}
		rc.Close()
	}
	rc, ok, err := z.open("xl/workbook.xml")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, extractErr(ExtractInvalidFile)
	}
	defer rc.Close()
	var sheets []xlsxSheet
	d := xmlDecoder(rc)
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, asExtractErr(err)
		}
		e, ok := tok.(xml.StartElement)
		if !ok || e.Name.Local != "sheet" {
			continue
		}
		var name, rid, sheetID string
		for _, a := range e.Attr {
			switch {
			case a.Name.Local == "name":
				name = a.Value
			case a.Name.Local == "id" && a.Name.Space != "":
				rid = a.Value
			case a.Name.Local == "sheetId":
				sheetID = a.Value
			}
		}
		target := rels[rid]
		if target == "" && sheetID != "" {
			target = "xl/worksheets/sheet" + sheetID + ".xml"
		}
		if target != "" {
			sheets = append(sheets, xlsxSheet{name: name, target: target})
		}
	}
	return sheets, nil
}

func xlsxSharedStrings(z *zipArchive) ([]string, error) {
	rc, ok, err := z.open("xl/sharedStrings.xml")
	if err != nil || !ok {
		return nil, err
	}
	defer rc.Close()
	var out []string
	var cur strings.Builder
	inT, phonetic := false, 0
	d := xmlDecoder(rc)
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, asExtractErr(err)
		}
		switch e := tok.(type) {
		case xml.StartElement:
			switch e.Name.Local {
			case "si":
				cur.Reset()
			case "rPh":
				phonetic++
			case "t":
				inT = phonetic == 0
			}
		case xml.EndElement:
			switch e.Name.Local {
			case "si":
				if len(out) >= maxSharedStrings {
					return nil, extractErr(ExtractTooLarge)
				}
				out = append(out, cur.String())
			case "rPh":
				phonetic--
			case "t":
				inT = false
			}
		case xml.CharData:
			if inT && cur.Len() < 32<<10 {
				cur.Write(e)
			}
		}
	}
	return out, nil
}

// columnIndex converts the letters of a cell reference such as "BC12" to a
// zero-based column, or -1.
func columnIndex(ref string) int {
	n := 0
	i := 0
	for ; i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z'; i++ {
		n = n*26 + int(ref[i]-'A'+1)
		if n > 1<<15 {
			return -1
		}
	}
	if i == 0 {
		return -1
	}
	return n - 1
}

func xlsxSheetText(z *zipArchive, target string, shared []string, t *textBuilder) error {
	rc, ok, err := z.open(target)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	defer rc.Close()
	d := xmlDecoder(rc)
	type cell struct {
		col int
		val string
	}
	var row []cell
	var ctype, ref string
	var val bytes.Buffer
	inV, inIs, inT := false, false, false
	header := true
	flush := func() {
		if len(row) == 0 {
			return
		}
		sort.SliceStable(row, func(i, j int) bool { return row[i].col < row[j].col })
		last := row[len(row)-1].col
		if last >= maxSheetColumns {
			last = maxSheetColumns - 1
		}
		vals := make([]string, last+1)
		extra := []string{}
		for _, c := range row {
			if c.col >= 0 && c.col <= last {
				vals[c.col] = c.val
			} else {
				extra = append(extra, c.val)
			}
		}
		vals = append(vals, extra...)
		empty := true
		for _, v := range vals {
			if v != "" {
				empty = false
				break
			}
		}
		if !empty {
			t.write("| " + strings.Join(escapeCells(vals), " | ") + " |\n")
			if header {
				t.write(tableSeparator(len(vals)))
				header = false
			}
		}
		row = row[:0]
	}
	next := 0
	for !t.full() {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return asExtractErr(err)
		}
		switch e := tok.(type) {
		case xml.StartElement:
			switch e.Name.Local {
			case "row":
				row, next = row[:0], 0
			case "c":
				ctype, ref = "", ""
				val.Reset()
				for _, a := range e.Attr {
					switch a.Name.Local {
					case "t":
						ctype = a.Value
					case "r":
						ref = a.Value
					}
				}
			case "v":
				inV = true
			case "is":
				inIs = true
			case "t":
				inT = inIs
			}
		case xml.EndElement:
			switch e.Name.Local {
			case "v":
				inV = false
			case "is":
				inIs = false
			case "t":
				inT = false
			case "c":
				col := columnIndex(ref)
				if col < 0 {
					col = next
				}
				next = col + 1
				if v := cellValue(ctype, val.String(), shared); v != "" && len(row) < 4*maxSheetColumns {
					row = append(row, cell{col: col, val: strings.Join(strings.Fields(v), " ")})
				}
			case "row":
				flush()
			}
		case xml.CharData:
			if (inV || inT) && val.Len() < 32<<10 {
				val.Write(e)
			}
		}
	}
	flush()
	return nil
}

func cellValue(ctype, raw string, shared []string) string {
	switch ctype {
	case "s":
		i, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || i < 0 || i >= len(shared) {
			return ""
		}
		return shared[i]
	case "b":
		if strings.TrimSpace(raw) == "1" {
			return "TRUE"
		}
		return "FALSE"
	}
	return raw
}
