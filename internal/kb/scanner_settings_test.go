package kb

import (
	"context"
	"strings"
	"testing"
)

func TestValidateClamAVAddress(t *testing.T) {
	for _, ok := range []string{"clamav:3310", "clamav.railway.internal:3310", "10.0.0.5:3310", "[::1]:3310"} {
		if err := ValidateClamAVAddress(ok); err != nil {
			t.Errorf("rejected %q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "clamav", "clamav:", ":3310", "tcp://clamav:3310", "user@clamav:3310", "cla mav:3310", "clamav:3310/x"} {
		if err := ValidateClamAVAddress(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestSetScanner(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	admin := loginAs(t, s, "admin@example.com")
	member := inviteAs(t, s, admin, "member@example.com")
	env := &fakeScanner{}
	s.Scanner = env

	// Nothing saved: the environment scanner applies.
	if got, source, err := s.effectiveScanner(ctx, s.DB); err != nil || got != env || source != "server" {
		t.Fatalf("default: %v %q %v", got, source, err)
	}
	// Only a global administrator may change it, and the address is validated.
	address := "clamav.internal:3310"
	if _, err := s.SetScanner(ctx, member, ScannerInput{Address: &address}); err == nil {
		t.Fatal("member changed the scanner")
	}
	if _, err := s.ScannerStatus(ctx, member); err == nil {
		t.Fatal("member read the scanner setting")
	}
	bad := "clamav"
	if _, err := s.SetScanner(ctx, admin, ScannerInput{Address: &bad}); err == nil {
		t.Fatal("accepted an address without a port")
	}
	// The administrator's address wins over the environment.
	if _, err := s.SetScanner(ctx, admin, ScannerInput{Address: &address}); err != nil {
		t.Fatal(err)
	}
	got, source, err := s.effectiveScanner(ctx, s.DB)
	if c, ok := got.(ClamAV); err != nil || !ok || c.Address != address || source != "admin" {
		t.Fatalf("saved: %v %q %v", got, source, err)
	}
	// An empty address turns scanning off even though the environment sets one.
	off := ""
	if _, err := s.SetScanner(ctx, admin, ScannerInput{Address: &off}); err != nil {
		t.Fatal(err)
	}
	if got, source, err = s.effectiveScanner(ctx, s.DB); err != nil || got != nil || source != "admin" {
		t.Fatalf("off: %v %q %v", got, source, err)
	}
	status := mustValue(t)(s.ScannerStatus(ctx, admin)).(map[string]any)
	if status["enabled"] != false || status["setting"] != "" || status["env_configured"] != true {
		t.Fatalf("status: %v", status)
	}
	// The audit trail records that scanning changed, never the private address.
	var details string
	if err := s.DB.QueryRow(`SELECT string_agg(details::text, ' ') FROM audit_events WHERE action='update_scanner'`).Scan(&details); err != nil ||
		strings.Contains(details, "clamav") || !strings.Contains(details, "enabled") {
		t.Fatalf("audit: %q %v", details, err)
	}
	// Null goes back to the server default.
	if _, err := s.SetScanner(ctx, admin, ScannerInput{}); err != nil {
		t.Fatal(err)
	}
	if got, source, _ = s.effectiveScanner(ctx, s.DB); got != env || source != "server" {
		t.Fatalf("reset: %v %q", got, source)
	}
}
