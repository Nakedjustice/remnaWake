package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// InfraServer is one rented hosting server tracked for infrastructure-cost
// monitoring. Price is a whole number in the server's own Currency (ISO 4217);
// PeriodMonths is the billing period (1 = monthly, 3 = quarterly). NextDueAt is
// the next payment due date (zero = not set). RemindedStage dedups payment
// reminders: 0 none, 1 pre-due sent, 2 overdue sent.
type InfraServer struct {
	ID            int64
	Name          string
	Hoster        string
	Country       string
	CPUCores      int
	RAMMB         int
	Price         int
	Currency      string
	PeriodMonths  int
	NextDueAt     time.Time
	RemindedStage int
	Notes         string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// infraDueArg renders NextDueAt for storage: NULL when unset, RFC3339 otherwise.
func infraDueArg(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatTime(t)
}

const infraServerColumns = `id, name, hoster, country, cpu_cores, ram_mb, price, currency,
	period_months, next_due_at, reminded_stage, notes, created_at, updated_at`

// scanInfraServer reads one row in infraServerColumns order.
func scanInfraServer(sc interface{ Scan(...any) error }, srv *InfraServer) error {
	var (
		due          sql.NullString
		created, upd string
	)
	if err := sc.Scan(&srv.ID, &srv.Name, &srv.Hoster, &srv.Country, &srv.CPUCores, &srv.RAMMB,
		&srv.Price, &srv.Currency, &srv.PeriodMonths, &due, &srv.RemindedStage, &srv.Notes,
		&created, &upd); err != nil {
		return err
	}
	if due.Valid {
		srv.NextDueAt, _ = parseTime(due.String)
	}
	srv.CreatedAt, _ = parseTime(created)
	srv.UpdatedAt, _ = parseTime(upd)
	return nil
}

// CreateInfraServer inserts a new server and returns its id.
func (s *Store) CreateInfraServer(ctx context.Context, srv InfraServer) (int64, error) {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO infra_servers
		  (name, hoster, country, cpu_cores, ram_mb, price, currency, period_months, next_due_at, reminded_stage, notes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, srv.Name, srv.Hoster, srv.Country, srv.CPUCores, srv.RAMMB, srv.Price, srv.Currency,
		srv.PeriodMonths, infraDueArg(srv.NextDueAt), srv.RemindedStage, srv.Notes, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListInfraServers returns every server, soonest payment due first, then
// servers without a due date, then by id.
func (s *Store) ListInfraServers(ctx context.Context) ([]InfraServer, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+infraServerColumns+` FROM infra_servers
		ORDER BY (next_due_at IS NULL), next_due_at ASC, id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []InfraServer
	for rows.Next() {
		var srv InfraServer
		if err := scanInfraServer(rows, &srv); err != nil {
			return nil, err
		}
		out = append(out, srv)
	}
	return out, rows.Err()
}

// GetInfraServer returns the server with id, or (nil, nil) when absent.
func (s *Store) GetInfraServer(ctx context.Context, id int64) (*InfraServer, error) {
	var srv InfraServer
	err := scanInfraServer(
		s.db.QueryRowContext(ctx, `SELECT `+infraServerColumns+` FROM infra_servers WHERE id = ?`, id),
		&srv,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &srv, nil
}

// UpdateInfraServer overwrites the editable fields of an existing server and
// resets reminded_stage to 0 (the due date or amount may have changed, so the
// reminder cycle re-arms). found is false when no row has the given id.
func (s *Store) UpdateInfraServer(ctx context.Context, srv InfraServer) (bool, error) {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(ctx, `
		UPDATE infra_servers SET
		  name = ?, hoster = ?, country = ?, cpu_cores = ?, ram_mb = ?, price = ?, currency = ?,
		  period_months = ?, next_due_at = ?, reminded_stage = 0, notes = ?, updated_at = ?
		WHERE id = ?
	`, srv.Name, srv.Hoster, srv.Country, srv.CPUCores, srv.RAMMB, srv.Price, srv.Currency,
		srv.PeriodMonths, infraDueArg(srv.NextDueAt), srv.Notes, now, srv.ID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteInfraServer removes a server. found is false when no row had the id.
func (s *Store) DeleteInfraServer(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM infra_servers WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetInfraServerRemindedStage records how far the payment-reminder cycle has
// progressed for a server (see InfraServer.RemindedStage).
func (s *Store) SetInfraServerRemindedStage(ctx context.Context, id int64, stage int) (bool, error) {
	now := formatTime(time.Now())
	res, err := s.db.ExecContext(ctx,
		`UPDATE infra_servers SET reminded_stage = ?, updated_at = ? WHERE id = ?`, stage, now, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
