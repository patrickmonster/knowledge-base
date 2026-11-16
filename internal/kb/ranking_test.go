package kb

import (
	"fmt"
	"testing"
)

func rankedHits(kinds ...string) []SearchHit {
	hits := make([]SearchHit, len(kinds))
	for i, kind := range kinds {
		hits[i] = SearchHit{DocumentID: fmt.Sprintf("%s-%02d", kind, i), Kind: kind}
	}
	return hits
}

func hitIDs(hits []SearchHit) []string {
	ids := make([]string, len(hits))
	for i, h := range hits {
		ids[i] = h.DocumentID
	}
	return ids
}

func TestFuseRanksPlacesCurrentKnowledgeAboveNearbyHistory(t *testing.T) {
	// History ranks first in both lists, a reference second: without the
	// offset the history would win.
	list := rankedHits("history", "reference")
	got := fuseRanks(list, list, 2)
	if got[0].Kind != "reference" || got[1].Kind != "history" {
		t.Fatalf("order = %v, want reference before history", hitIDs(got))
	}
}

func TestFuseRanksKeepsHistoryMatchedByBothLists(t *testing.T) {
	// A history found by keyword and vector search still beats a reference
	// found by only one of them: the offset demotes, it does not hide.
	history := SearchHit{DocumentID: "h", Kind: "history"}
	reference := SearchHit{DocumentID: "r", Kind: "reference"}
	got := fuseRanks([]SearchHit{history}, []SearchHit{reference, history}, 2)
	if got[0].DocumentID != "h" {
		t.Fatalf("order = %v, want the history first", hitIDs(got))
	}
}

func TestFuseRanksIsDeterministic(t *testing.T) {
	lexical := rankedHits("history", "decision", "history", "brief", "glossary")
	semantic := rankedHits("brief", "history", "reference", "history")
	first := hitIDs(fuseRanks(lexical, semantic, 8))
	for range 20 {
		if got := hitIDs(fuseRanks(lexical, semantic, 8)); fmt.Sprint(got) != fmt.Sprint(first) {
			t.Fatalf("order changed: %v then %v", first, got)
		}
	}
}

func TestCapHistorySources(t *testing.T) {
	tests := []struct {
		name  string
		hits  []SearchHit
		limit int
		want  []string
	}{
		{"caps history and fills with other kinds",
			rankedHits("history", "history", "history", "history", "reference", "history", "decision"), 5,
			[]string{"history-00", "history-01", "history-02", "reference-04", "decision-06"}},
		{"backfills skipped history in order when others run out",
			rankedHits("history", "history", "history", "history", "history", "brief"), 5,
			[]string{"history-00", "history-01", "history-02", "brief-05", "history-03"}},
		{"keeps order and limit without history",
			rankedHits("brief", "reference", "decision"), 2,
			[]string{"brief-00", "reference-01"}},
		{"empty", nil, 8, []string{}},
	}
	for _, tt := range tests {
		got := hitIDs(capHistorySources(tt.hits, tt.limit, 3))
		if fmt.Sprint(got) != fmt.Sprint(tt.want) {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}
