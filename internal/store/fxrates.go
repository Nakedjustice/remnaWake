package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// FxRate is the cached exchange rate for one ISO 4217 currency, expressed as the
// number of base-currency units per 1 unit of Currency. AutoRate is fetched from
// a live source; ManualRate is the admin-entered fallback used when no auto rate
// is available. The Has* flags distinguish an unset rate from a genuine zero.
type FxRate struct {
	Currency      string
	AutoRate      float64
	HasAuto       bool
	AutoUpdatedAt time.Time
	ManualRate    float64
	HasManual     bool
	UpdatedAt     time.Time
}

// ListFxRates returns every cached rate, ordered by currency code.
func (s *Store) ListFxRates(ctx context.Context) ([]FxRate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT currency, auto_rate, auto_updated_at, manual_rate, updated_at
		FROM fx_rates ORDER BY currency ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FxRate
	for rows.Next() {
		fx, err := scanFxRate(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, fx)
	}
	return out, rows.Err()
}

// GetFxRate returns the cached rate for currency, or (nil, nil) when absent.
func (s *Store) GetFxRate(ctx context.Context, currency string) (*FxRate, error) {
	fx, err := scanFxRate(s.db.QueryRowContext(ctx, `
		SELECT currency, auto_rate, auto_updated_at, manual_rate, updated_at
		FROM fx_rates WHERE currency = ?
	`, currency))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &fx, nil
}

// UpsertAutoFxRate stores the freshly fetched rate for currency, leaving any
// manual fallback untouched.
func (s *Store) UpsertAutoFxRate(ctx context.Context, currency string, rate float64, at time.Time) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO fx_rates (currency, auto_rate, auto_updated_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(currency) DO UPDATE SET
		  auto_rate = excluded.auto_rate,
		  auto_updated_at = excluded.auto_updated_at,
		  updated_at = excluded.updated_at
	`, currency, rate, formatTime(at), now)
	return err
}

// UpsertManualFxRate stores the admin-entered fallback rate for currency,
// leaving any fetched auto rate untouched.
func (s *Store) UpsertManualFxRate(ctx context.Context, currency string, rate float64) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO fx_rates (currency, manual_rate, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(currency) DO UPDATE SET
		  manual_rate = excluded.manual_rate,
		  updated_at = excluded.updated_at
	`, currency, rate, now)
	return err
}

func scanFxRate(sc interface{ Scan(...any) error }) (FxRate, error) {
	var (
		fx         FxRate
		autoRate   sql.NullFloat64
		autoAt     sql.NullString
		manualRate sql.NullFloat64
		updated    string
	)
	if err := sc.Scan(&fx.Currency, &autoRate, &autoAt, &manualRate, &updated); err != nil {
		return FxRate{}, err
	}
	if autoRate.Valid {
		fx.HasAuto = true
		fx.AutoRate = autoRate.Float64
	}
	if autoAt.Valid {
		fx.AutoUpdatedAt, _ = parseTime(autoAt.String)
	}
	if manualRate.Valid {
		fx.HasManual = true
		fx.ManualRate = manualRate.Float64
	}
	fx.UpdatedAt, _ = parseTime(updated)
	return fx, nil
}
