package kb

import (
	"fmt"
	"slices"
	"time"
)

// Recheck warns that an active reference or brief has not been updated for
// DefaultStaleDays. It uses the same rule as list_documents' needs_recheck and
// is attached by get_document, search and task context; it is never stored.
type Recheck struct {
	NeedsRecheck    bool   `json:"needs_recheck"`
	DaysSinceUpdate int    `json:"days_since_update"`
	StaleAfterDays  int    `json:"stale_after_days"`
	Message         string `json:"message"`
}

// recheckFor returns a warning when kind/status/updatedAt make the document
// stale at now, and nil otherwise (including unparseable timestamps).
func recheckFor(kind, status, updatedAt string, now time.Time) *Recheck {
	if status != "active" || !slices.Contains(staleKinds, kind) {
		return nil
	}
	updated, err := time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return nil
	}
	if !updated.Before(now.AddDate(0, 0, -DefaultStaleDays)) {
		return nil
	}
	days := int(now.Sub(updated).Hours() / 24)
	return &Recheck{
		NeedsRecheck:    true,
		DaysSinceUpdate: days,
		StaleAfterDays:  DefaultStaleDays,
		Message:         fmt.Sprintf("Not updated for %d days. Verify it still matches the current state before relying on it; if it does, update_document to refresh it.", days),
	}
}

// markRecheck annotates stale hits in place. Ranking is unchanged: an old
// reference can still be the most relevant source, it just needs verifying.
func markRecheck(hits []SearchHit, now time.Time) {
	for i := range hits {
		hits[i].Recheck = recheckFor(hits[i].Kind, hits[i].Status, hits[i].UpdatedAt, now)
	}
}
