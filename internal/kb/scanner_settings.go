package kb

import (
	"context"
	"database/sql"
	"errors"
	"net"
	"strings"
)

// scannerSetting is the stored scanner address. Present=false means no row, so
// the environment value (Store.Scanner) applies; an empty Address turns
// scanning off.
type scannerSetting struct {
	Present bool
	Address string
}

func readScannerSetting(ctx context.Context, q Querier) (scannerSetting, error) {
	var address string
	err := q.QueryRowContext(ctx, `SELECT clamav_address FROM scanner_settings WHERE id`).Scan(&address)
	if errors.Is(err, sql.ErrNoRows) {
		return scannerSetting{}, nil
	}
	if err != nil {
		return scannerSetting{}, err
	}
	return scannerSetting{Present: true, Address: address}, nil
}

// effectiveScanner returns the scanner uploads must pass: the administrator's
// setting wins over the environment default (Store.Scanner). It is read on every
// use so a change in the web console applies without a restart.
func (s *Store) effectiveScanner(ctx context.Context, q Querier) (MalwareScanner, string, error) {
	setting, err := readScannerSetting(ctx, q)
	if err != nil {
		return nil, "", err
	}
	if !setting.Present {
		if s.Scanner == nil {
			return nil, "server", nil
		}
		return s.Scanner, "server", nil
	}
	if setting.Address == "" {
		return nil, "admin", nil
	}
	return ClamAV{Address: setting.Address}, "admin", nil
}

// ValidateClamAVAddress checks host:port the same way the environment value is
// checked at startup.
func ValidateClamAVAddress(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || port == "" || strings.ContainsAny(address, " \t\r\n/@") {
		return problem("INVALID_INPUT", "address must be host:port.")
	}
	return nil
}

// ScannerInput is the web console form. A null address goes back to the server
// default (CLAMAV_ADDRESS); an empty string turns scanning off.
type ScannerInput struct {
	Address *string `json:"address"`
}

// ScannerStatus reports which scanner is in effect and where the setting comes
// from. The address itself is only shown to the global administrator who asked.
func (s *Store) ScannerStatus(ctx context.Context, actor Identity) (any, error) {
	return s.Perform(ctx, actor, "view_scanner", "", false, true, func(tx *sql.Tx) (any, error) {
		return s.scannerSettingsView(ctx, tx)
	})
}

func (s *Store) scannerSettingsView(ctx context.Context, q Querier) (map[string]any, error) {
	setting, err := readScannerSetting(ctx, q)
	if err != nil {
		return nil, err
	}
	scanner, source, err := s.effectiveScanner(ctx, q)
	if err != nil {
		return nil, err
	}
	view := map[string]any{"source": source, "enabled": scanner != nil, "env_configured": s.Scanner != nil}
	if setting.Present {
		view["setting"] = setting.Address
	} else {
		view["setting"] = nil
	}
	if c, ok := scanner.(ClamAV); ok {
		view["address"] = c.Address
	}
	return view, nil
}

// SetScanner saves the scanner address. Only a global administrator in a web
// session may call it (Perform rejects keys for admin operations). The audit
// record keeps whether scanning is on, never the private address.
func (s *Store) SetScanner(ctx context.Context, actor Identity, in ScannerInput) (any, error) {
	if in.Address != nil {
		trimmed := strings.TrimSpace(*in.Address)
		in.Address = &trimmed
	}
	enabled := in.Address == nil && s.Scanner != nil || in.Address != nil && *in.Address != ""
	ctx = auditInput(ctx, map[string]any{"enabled": enabled, "remove": in.Address == nil})
	return s.Perform(ctx, actor, "update_scanner", "", true, true, func(tx *sql.Tx) (any, error) {
		switch {
		case in.Address == nil:
			if _, err := tx.ExecContext(ctx, `DELETE FROM scanner_settings`); err != nil {
				return nil, err
			}
		default:
			if *in.Address != "" {
				if err := ValidateClamAVAddress(*in.Address); err != nil {
					return nil, err
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO scanner_settings(id,clamav_address,updated_by) VALUES(true,$1,$2)
				ON CONFLICT(id) DO UPDATE SET clamav_address=EXCLUDED.clamav_address,updated_by=EXCLUDED.updated_by,updated_at=now()`,
				*in.Address, actor.User.ID); err != nil {
				return nil, err
			}
		}
		return s.scannerSettingsView(ctx, tx)
	})
}
