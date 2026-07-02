package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Plan is an admin-defined tariff preset: the panel settings stamped onto a
// user when a purchase under this plan is confirmed. Zero traffic/device
// limits mean unlimited; an empty squad UUID means "use the default squad";
// an empty tag leaves/clears the panel tag. The built-in 'standard' plan is
// never stored here.
type Plan struct {
	Code        string
	Name        string
	Tag         string
	SquadUUID   string
	TrafficGB   int
	DeviceLimit int
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (s *Store) UpsertPlan(ctx context.Context, p Plan) error {
	now := formatTime(time.Now())
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO plans (code, name, tag, squad_uuid, traffic_gb, device_limit, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(code) DO UPDATE SET
			name = excluded.name, tag = excluded.tag, squad_uuid = excluded.squad_uuid,
			traffic_gb = excluded.traffic_gb, device_limit = excluded.device_limit,
			updated_at = excluded.updated_at
	`, p.Code, p.Name, p.Tag, p.SquadUUID, p.TrafficGB, p.DeviceLimit, now, now)
	return err
}

func (s *Store) GetPlan(ctx context.Context, code string) (*Plan, error) {
	var (
		p            Plan
		created, upd string
	)
	err := s.db.QueryRowContext(ctx, `
		SELECT code, name, tag, squad_uuid, traffic_gb, device_limit, created_at, updated_at
		FROM plans WHERE code = ?
	`, code).Scan(&p.Code, &p.Name, &p.Tag, &p.SquadUUID, &p.TrafficGB, &p.DeviceLimit, &created, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.CreatedAt, _ = parseTime(created)
	p.UpdatedAt, _ = parseTime(upd)
	return &p, nil
}

func (s *Store) ListPlans(ctx context.Context) ([]Plan, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT code, name, tag, squad_uuid, traffic_gb, device_limit, created_at, updated_at
		FROM plans ORDER BY code ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Plan
	for rows.Next() {
		var (
			p            Plan
			created, upd string
		)
		if err := rows.Scan(&p.Code, &p.Name, &p.Tag, &p.SquadUUID, &p.TrafficGB, &p.DeviceLimit, &created, &upd); err != nil {
			return nil, err
		}
		p.CreatedAt, _ = parseTime(created)
		p.UpdatedAt, _ = parseTime(upd)
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeletePlan removes a plan together with its tariff grid so orphaned prices
// cannot resurface if the code is ever reused.
func (s *Store) DeletePlan(ctx context.Context, code string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `DELETE FROM plans WHERE code = ?`, code)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tariffs WHERE plan = ?`, code); err != nil {
		return false, err
	}
	return true, tx.Commit()
}
