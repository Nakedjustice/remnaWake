package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PlanStandard is the built-in plan code every pre-existing tariff belongs to.
// It has no row in the plans table; the payments layer synthesizes it.
const PlanStandard = "standard"

type Tariff struct {
	Plan      string
	Months    int
	Price     int
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *Store) UpsertTariff(ctx context.Context, plan string, months, price int) error {
	if plan == "" {
		plan = PlanStandard
	}
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tariffs (plan, months, price, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(plan, months) DO UPDATE SET price = excluded.price, updated_at = excluded.updated_at
	`, plan, months, price, now, now)
	return err
}

func (s *Store) ListTariffs(ctx context.Context, plan string) ([]Tariff, error) {
	if plan == "" {
		plan = PlanStandard
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT plan, months, price, created_at, updated_at FROM tariffs
		WHERE plan = ? ORDER BY months ASC
	`, plan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTariffs(rows)
}

// ListAllTariffs returns every tariff across all plans, ordered by plan then
// months.
func (s *Store) ListAllTariffs(ctx context.Context) ([]Tariff, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT plan, months, price, created_at, updated_at FROM tariffs
		ORDER BY plan ASC, months ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTariffs(rows)
}

func scanTariffs(rows *sql.Rows) ([]Tariff, error) {
	var out []Tariff
	for rows.Next() {
		var (
			t            Tariff
			created, upd string
		)
		if err := rows.Scan(&t.Plan, &t.Months, &t.Price, &created, &upd); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = parseTime(created)
		t.UpdatedAt, _ = parseTime(upd)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTariff(ctx context.Context, plan string, months int) (*Tariff, error) {
	if plan == "" {
		plan = PlanStandard
	}
	var (
		t            Tariff
		created, upd string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT plan, months, price, created_at, updated_at FROM tariffs
		WHERE plan = ? AND months = ?
	`, plan, months).Scan(&t.Plan, &t.Months, &t.Price, &created, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.CreatedAt, _ = parseTime(created)
	t.UpdatedAt, _ = parseTime(upd)
	return &t, nil
}

func (s *Store) DeleteTariff(ctx context.Context, plan string, months int) (bool, error) {
	if plan == "" {
		plan = PlanStandard
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM tariffs WHERE plan = ? AND months = ?`, plan, months)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
