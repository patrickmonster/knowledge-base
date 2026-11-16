package kb

import (
	"encoding/xml"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// XLSX stores dates as serial numbers (days since an epoch) and marks them as
// dates only through the cell's number format. These helpers read the number
// formats from xl/styles.xml so extracted text shows 2026-11-15 instead of
// 46341.

// dateKind says which parts of a date/time a number format displays.
type dateKind uint8

const (
	dateNone    dateKind = 0
	datePart    dateKind = 1 << 0
	timePart    dateKind = 1 << 1
	secondsPart dateKind = 1 << 2
)

// maxCellStyles caps the cellXfs entries read from styles.xml.
const maxCellStyles = 1 << 16

// builtinDateFormats lists the built-in number format IDs that display dates
// or times. 14–22 and 45–47 are fixed by ECMA-376; 27–36 and 50–58 are
// locale-specific East Asian date formats (for example yyyy"년" m"월" d"일" in
// Korean Excel).
func builtinDateFormat(id int) dateKind {
	switch {
	case id >= 14 && id <= 17, id >= 27 && id <= 31, id >= 34 && id <= 36, id >= 50 && id <= 58:
		return datePart
	case id == 18, id == 20:
		return timePart
	case id == 19, id == 21, id == 45, id == 46, id == 47:
		return timePart | secondsPart
	case id == 22:
		return datePart | timePart
	case id == 32, id == 33:
		return timePart
	}
	return dateNone
}

// classifyNumFmt decides from a custom format code whether it shows a date,
// a time or both. Quoted text, escaped characters, colors and conditions are
// ignored; only the first section (positive numbers) counts.
func classifyNumFmt(code string) dateKind {
	if i := strings.IndexByte(code, ';'); i >= 0 {
		code = code[:i]
	}
	var b strings.Builder
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch c {
		case '"':
			j := strings.IndexByte(code[i+1:], '"')
			if j < 0 {
				i = len(code)
			} else {
				i += j + 1
			}
		case '\\', '_', '*':
			i++ // the next character is literal or padding
		case '[':
			j := strings.IndexByte(code[i+1:], ']')
			if j < 0 {
				i = len(code)
				break
			}
			inner := strings.ToLower(code[i+1 : i+1+j])
			// Elapsed time such as [h]:mm is a duration, still a time display.
			if inner != "" && strings.Trim(inner, "hms") == "" {
				b.WriteString(inner)
			}
			i += j + 1
		default:
			b.WriteByte(c)
		}
	}
	f := strings.ToLower(b.String())
	if f == "general" || f == "" {
		return dateNone
	}
	var k dateKind
	if strings.ContainsAny(f, "yd") || strings.Contains(f, "e") && !strings.ContainsAny(f, "0#?") {
		k |= datePart
	}
	if strings.ContainsAny(f, "hs") {
		k |= timePart
	}
	if strings.Contains(f, "s") {
		k |= secondsPart
	}
	// A lone "m"/"mm" without y, d, h or s is a month display (e.g. "mmm").
	if k == dateNone && strings.Contains(f, "m") && !strings.ContainsAny(f, "0#?") {
		k = datePart
	}
	if k&datePart == 0 && k&timePart == 0 {
		return dateNone
	}
	return k
}

// xlsxCellStyles reads xl/styles.xml and returns, for each cellXfs index (the
// cell's s attribute), what part of a date it displays. A workbook without
// styles has no date cells.
func xlsxCellStyles(z *zipArchive) ([]dateKind, error) {
	rc, ok, err := z.open("xl/styles.xml")
	if err != nil || !ok {
		return nil, err
	}
	defer rc.Close()
	custom := map[int]dateKind{}
	var xfs []int
	inCellXfs := false
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
			case "numFmt":
				id, code := -1, ""
				for _, a := range e.Attr {
					switch a.Name.Local {
					case "numFmtId":
						if n, err := strconv.Atoi(a.Value); err == nil {
							id = n
						}
					case "formatCode":
						code = a.Value
					}
				}
				if id >= 0 && len(custom) < maxCellStyles {
					custom[id] = classifyNumFmt(code)
				}
			case "cellXfs":
				inCellXfs = true
			case "xf":
				if !inCellXfs {
					continue
				}
				if len(xfs) >= maxCellStyles {
					return nil, extractErr(ExtractTooLarge)
				}
				id := 0
				for _, a := range e.Attr {
					if a.Name.Local == "numFmtId" {
						if n, err := strconv.Atoi(a.Value); err == nil {
							id = n
						}
					}
				}
				xfs = append(xfs, id)
			}
		case xml.EndElement:
			if e.Name.Local == "cellXfs" {
				inCellXfs = false
			}
		}
	}
	out := make([]dateKind, len(xfs))
	for i, id := range xfs {
		if k, ok := custom[id]; ok {
			out[i] = k
		} else {
			out[i] = builtinDateFormat(id)
		}
	}
	return out, nil
}

// maxExcelSerial is 9999-12-31, the last date Excel can show.
const maxExcelSerial = 2958465

// formatExcelDate turns a serial number into text for the given date kind. It
// returns false when the value is not a number Excel would show as a date.
func formatExcelDate(raw string, kind dateKind, date1904 bool) (string, bool) {
	if kind == dateNone {
		return "", false
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > maxExcelSerial+1 {
		return "", false
	}
	if kind&datePart != 0 && v < 1 && !date1904 {
		// Excel shows serial 0 as the non-existent 1900-01-00.
		if kind&timePart == 0 {
			return "", false
		}
		kind &^= datePart
	}
	secs := int64(math.Round(v * 86400))
	var base time.Time
	switch {
	case date1904:
		base = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	case secs < 61*86400:
		// Before Excel's fictional 1900-02-29 (serial 60) the epoch is
		// 1899-12-31; from serial 61 on, 1899-12-30 absorbs that extra day.
		base = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
	default:
		base = time.Date(1899, 12, 30, 0, 0, 0, 0, time.UTC)
	}
	tm := base.Add(time.Duration(secs) * time.Second)
	layout := ""
	if kind&datePart != 0 {
		layout = "2006-01-02"
	}
	if kind&timePart != 0 {
		if layout != "" {
			layout += " "
		}
		if kind&secondsPart != 0 {
			layout += "15:04:05"
		} else {
			layout += "15:04"
		}
	}
	return tm.Format(layout), true
}
