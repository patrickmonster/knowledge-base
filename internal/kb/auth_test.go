package kb

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPasswordHashing(t *testing.T) {
	ctx := context.Background()
	password := "a-long-password-한글"
	a, err := HashPassword(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	if a == b || strings.Contains(a, password) {
		t.Fatal("password must use a random salt and must not be stored verbatim")
	}
	if !VerifyPassword(ctx, a, password) || VerifyPassword(ctx, a, "wrong-password") || VerifyPassword(ctx, "malformed", password) {
		t.Fatal("password verification failed")
	}
	if _, err := HashPassword(ctx, "short"); err == nil {
		t.Fatal("short password accepted")
	}
}
func TestKoreanRetrievalPreparation(t *testing.T) {
	terms := strings.Join(Lexemes("결제를 먼저 구현했습니다. API"), " ")
	if !strings.Contains(terms, "결제") || !strings.Contains(terms, "api") {
		t.Fatal(terms)
	}
	q := QueryText("결제'); DROP TABLE users; --")
	if strings.Contains(q, ";") || strings.Contains(q, ")") {
		t.Fatal("raw query operators survived", q)
	}
	input := strings.Repeat("가😀나", 1200)
	chunks := SplitChunks(input)
	if len(chunks) < 2 {
		t.Fatal("expected several chunks")
	}
	for _, c := range chunks {
		if !utf8.ValidString(c) || len([]rune(c)) > 1600 {
			t.Fatal("invalid chunk")
		}
	}
}

func TestIDsMustBeCanonical(t *testing.T) {
	id := "abcdef12-3456-4789-abcd-123456789012"
	if validID(id) != nil {
		t.Fatal("canonical UUID rejected")
	}
	for _, input := range []string{strings.ToUpper(id), strings.ReplaceAll(id, "-", ""), "urn:uuid:" + id} {
		if validID(input) == nil {
			t.Fatal("noncanonical UUID can bypass identity comparisons", input)
		}
	}
}
