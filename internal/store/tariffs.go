package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Tariff struct {
	Months    int
	Price     int
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (s *Store) UpsertTariff(ctx context.Context, months, price int) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tariffs (months, price, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(months) DO UPDATE SET price = excluded.price, updated_at = excluded.updated_at
	`, months, price, now, now)
	return err
}

func (s *Store) ListTariffs(ctx context.Context) ([]Tariff, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT months, price, created_at, updated_at FROM tariffs ORDER BY months ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Tariff
	for rows.Next() {
		var (
			t            Tariff
			created, upd string
		)
		if err := rows.Scan(&t.Months, &t.Price, &created, &upd); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = parseTime(created)
		t.UpdatedAt, _ = parseTime(upd)
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) GetTariff(ctx context.Context, months int) (*Tariff, error) {
	var (
		t            Tariff
		created, upd string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT months, price, created_at, updated_at FROM tariffs WHERE months = ?
	`, months).Scan(&t.Months, &t.Price, &created, &upd)
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

func (s *Store) DeleteTariff(ctx context.Context, months int) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tariffs WHERE months = ?`, months)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
