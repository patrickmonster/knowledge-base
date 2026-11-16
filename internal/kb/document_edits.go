package kb

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	maxDocumentEdits     = 50
	maxDocumentEditChars = 20000
)

// DocumentEdit replaces one exact occurrence of OldStr with NewStr.
type DocumentEdit struct {
	OldStr string `json:"old_str" jsonschema:"Exact text to replace, copied from the current content; must occur exactly once. 1–20000 characters"`
	NewStr string `json:"new_str" jsonschema:"Replacement text; empty deletes old_str. Up to 20000 characters"`
}

// EditedDocument is the update_document response when edits were applied.
type EditedDocument struct {
	Document
	EditsApplied int `json:"edits_applied"`
}

// applyDocumentEdits applies edits in order to content. Every edit must match
// exactly once in the content as left by the previous edits; otherwise nothing
// is applied and the error names the failing edit.
func applyDocumentEdits(content string, edits []DocumentEdit) (string, error) {
	if len(edits) > maxDocumentEdits {
		return "", problem("INVALID_INPUT", fmt.Sprintf("At most %d edits are allowed.", maxDocumentEdits))
	}
	for i, e := range edits {
		if e.OldStr == "" || utf8.RuneCountInString(e.OldStr) > maxDocumentEditChars {
			return "", problem("INVALID_INPUT", fmt.Sprintf("edits[%d].old_str must have 1–%d characters.", i, maxDocumentEditChars))
		}
		if utf8.RuneCountInString(e.NewStr) > maxDocumentEditChars {
			return "", problem("INVALID_INPUT", fmt.Sprintf("edits[%d].new_str must have at most %d characters.", i, maxDocumentEditChars))
		}
	}
	for i, e := range edits {
		at := strings.Index(content, e.OldStr)
		if at < 0 {
			return "", problem("EDIT_NOT_APPLIED", fmt.Sprintf("edits[%d].old_str was not found in the content (after the edits before it). Nothing was saved; read the current document and copy the text exactly.", i))
		}
		// Overlapping matches count too: look again one byte after the first match start.
		if strings.Contains(content[at+1:], e.OldStr) {
			return "", problem("EDIT_NOT_APPLIED", fmt.Sprintf("edits[%d].old_str matches more than once (%d times). Nothing was saved; include more surrounding text so it matches exactly once.", i, countOverlapping(content, e.OldStr)))
		}
		content = content[:at] + e.NewStr + content[at+len(e.OldStr):]
	}
	return content, nil
}

func countOverlapping(s, sub string) int {
	n := 0
	for {
		at := strings.Index(s, sub)
		if at < 0 {
			return n
		}
		n++
		s = s[at+1:]
	}
}
