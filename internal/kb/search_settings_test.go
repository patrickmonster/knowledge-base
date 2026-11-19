package kb

import (
	"context"
	"testing"
)

func floatp(v float64) *float64 { return &v }

func TestParseSearchPolicy(t *testing.T) {
	p, err := ParseSearchPolicy("")
	if err != nil || p.IssueScoreFloor != defaultIssueScoreFloor {
		t.Fatalf("default floor: %+v %v", p, err)
	}
	for _, ok := range []struct {
		raw  string
		want float64
	}{{"0", 0}, {"0.1", 0.1}, {"0.62", 0.62}, {"0.95", 0.95}} {
		if p, err := ParseSearchPolicy(ok.raw); err != nil || p.IssueScoreFloor != ok.want {
			t.Fatalf("%q: %+v %v", ok.raw, p, err)
		}
	}
	for _, bad := range []string{"x", "-1", "0.05", "1", "1.5"} {
		if _, err := ParseSearchPolicy(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestSetSearchSettings(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	admin := loginAs(t, s, "admin@example.com")
	member := inviteAs(t, s, admin, "search-member@example.com")
	s.Search = SearchPolicy{IssueScoreFloor: 0.4}

	// Nothing saved: the server (environment) default applies.
	p, src, err := s.effectiveSearch(ctx, s.DB)
	if err != nil || p.IssueScoreFloor != 0.4 || src["issue_score_floor"] != "server" {
		t.Fatalf("defaults: %+v %v %v", p, src, err)
	}
	// Only a global administrator may change it, and values are validated.
	if _, err := s.SetSearchSettings(ctx, member, SearchSettingsInput{IssueScoreFloor: floatp(0.5)}); err == nil {
		t.Fatal("member changed the search settings")
	}
	for _, bad := range []float64{-0.1, 0.05, 1, 2} {
		if _, err := s.SetSearchSettings(ctx, admin, SearchSettingsInput{IssueScoreFloor: floatp(bad)}); err == nil {
			t.Fatalf("accepted %g", bad)
		}
	}
	if _, err := s.SetSearchSettings(ctx, admin, SearchSettingsInput{IssueScoreFloor: floatp(0.62)}); err != nil {
		t.Fatal(err)
	}
	if p, src, err = s.effectiveSearch(ctx, s.DB); err != nil || p.IssueScoreFloor != 0.62 || src["issue_score_floor"] != "admin" {
		t.Fatalf("saved: %+v %v %v", p, src, err)
	}
	status := mustValue(t)(s.SearchSettingsStatus(ctx, admin)).(map[string]any)
	if status["policy"].(SearchPolicy).IssueScoreFloor != 0.62 || status["updated_at"] == nil {
		t.Fatalf("status: %v", status)
	}
	var audited int
	s.DB.QueryRow(`SELECT count(*) FROM audit_events WHERE action='update_search_settings' AND details->>'issue_score_floor'='0.62'`).Scan(&audited)
	if audited != 1 {
		t.Fatalf("audit event count %d", audited)
	}
	// 0 is a floor of its own (no filtering), null goes back to the default.
	if _, err := s.SetSearchSettings(ctx, admin, SearchSettingsInput{IssueScoreFloor: floatp(0)}); err != nil {
		t.Fatal(err)
	}
	if p, src, err = s.effectiveSearch(ctx, s.DB); err != nil || p.IssueScoreFloor != 0 || src["issue_score_floor"] != "admin" {
		t.Fatalf("floor off: %+v %v %v", p, src, err)
	}
	if _, err := s.SetSearchSettings(ctx, admin, SearchSettingsInput{}); err != nil {
		t.Fatal(err)
	}
	if p, src, err = s.effectiveSearch(ctx, s.DB); err != nil || p.IssueScoreFloor != 0.4 || src["issue_score_floor"] != "server" {
		t.Fatalf("cleared: %+v %v %v", p, src, err)
	}
}

func TestAboveIssueScoreFloor(t *testing.T) {
	hits := []IssueSearchHit{{IssueID: "a", Score: 0.9}, {IssueID: "b", Score: 0.5}, {IssueID: "c", Score: 0.49}}
	if got := aboveIssueScoreFloor(hits, 0); len(got) != 3 {
		t.Fatalf("no floor keeps everything: %+v", got)
	}
	got := aboveIssueScoreFloor(hits, 0.5)
	if len(got) != 2 || got[0].IssueID != "a" || got[1].IssueID != "b" {
		t.Fatalf("floor is inclusive: %+v", got)
	}
	if len(hits) != 3 || hits[2].IssueID != "c" {
		t.Fatalf("the input list must not be modified: %+v", hits)
	}
}
