package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SupportMessage is one line in a user<->admin support ticket. Messages are
// grouped by TicketID; UserTelegramID identifies the user who owns the ticket.
type SupportMessage struct {
	ID               int64
	TicketID         int64
	UserTelegramID   int64
	UserUsername     string
	FromAdmin        bool  // false = user wrote, true = admin wrote
	AuthorTelegramID int64 // which admin replied (0 for user messages)
	Text             string
	CreatedAt        time.Time
	ReadByUser       bool
	ReadByAdmin      bool
}

// SupportTicket is one numbered support request. Status is a one-way machine:
// 'open' -> 'closed' (via CloseSupportTicket's conditional UPDATE); a closed
// ticket never reopens — a follow-up message starts a new ticket. The Last*
// and Unread fields are filled only by the list queries.
type SupportTicket struct {
	ID             int64
	UserTelegramID int64
	UserUsername   string
	Subject        string
	Status         string // "open" | "closed"
	CreatedAt      time.Time
	ClosedAt       *time.Time
	ClosedBy       string // "user" | "admin" ('' while open)

	LastText      string
	LastAt        time.Time
	LastFromAdmin bool
	Unread        int // user view: unread admin replies; admin view: unread user messages
}

// SupportConversation summarizes one open thread for the admin conversation
// list: who it belongs to, the latest line and how many user messages the
// admins have not read yet.
type SupportConversation struct {
	UserTelegramID int64
	UserUsername   string
	LastText       string
	LastAt         time.Time
	Unread         int // user messages with read_by_admin = 0
}

// AddSupportMessage appends a message to a conversation. The sender's own copy
// is marked read (the user's own messages are read by the user; an admin's
// replies are read by the admins).
func (s *Store) AddSupportMessage(ctx context.Context, m SupportMessage) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO support_messages
			(ticket_id, user_telegram_id, user_username, from_admin, author_telegram_id, text, created_at, read_by_user, read_by_admin)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, m.TicketID, m.UserTelegramID, m.UserUsername, boolToInt(m.FromAdmin), m.AuthorTelegramID,
		m.Text, formatTime(time.Now()), boolToInt(!m.FromAdmin), boolToInt(m.FromAdmin))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AddSupportMessageToOpenTicket inserts the message only while its ticket is
// still open (the INSERT..SELECT..WHERE EXISTS is the atomic guard against a
// concurrent close). ok=false means the ticket was closed or unknown and
// nothing was stored.
func (s *Store) AddSupportMessageToOpenTicket(ctx context.Context, m SupportMessage) (int64, bool, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO support_messages
			(ticket_id, user_telegram_id, user_username, from_admin, author_telegram_id, text, created_at, read_by_user, read_by_admin)
		SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?
		WHERE EXISTS (SELECT 1 FROM support_tickets WHERE id = ? AND status = 'open')
	`, m.TicketID, m.UserTelegramID, m.UserUsername, boolToInt(m.FromAdmin), m.AuthorTelegramID,
		m.Text, formatTime(time.Now()), boolToInt(!m.FromAdmin), boolToInt(m.FromAdmin), m.TicketID)
	if err != nil {
		return 0, false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return 0, false, err
	}
	id, err := res.LastInsertId()
	return id, true, err
}

// ListSupportMessages returns a conversation's messages in chronological order.
func (s *Store) ListSupportMessages(ctx context.Context, userTelegramID int64) ([]SupportMessage, error) {
	return s.listSupportMessages(ctx, `user_telegram_id = ?`, userTelegramID)
}

// ListSupportMessagesByTicket returns one ticket's messages in chronological order.
func (s *Store) ListSupportMessagesByTicket(ctx context.Context, ticketID int64) ([]SupportMessage, error) {
	return s.listSupportMessages(ctx, `ticket_id = ?`, ticketID)
}

func (s *Store) listSupportMessages(ctx context.Context, where string, arg any) ([]SupportMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, ticket_id, user_telegram_id, user_username, from_admin, author_telegram_id, text, created_at, read_by_user, read_by_admin
		FROM support_messages
		WHERE `+where+`
		ORDER BY created_at ASC, id ASC
	`, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SupportMessage
	for rows.Next() {
		var (
			m                            SupportMessage
			created                      string
			fromAdmin, readUser, readAdm int
		)
		if err := rows.Scan(&m.ID, &m.TicketID, &m.UserTelegramID, &m.UserUsername, &fromAdmin,
			&m.AuthorTelegramID, &m.Text, &created, &readUser, &readAdm); err != nil {
			return nil, err
		}
		m.FromAdmin = fromAdmin != 0
		m.ReadByUser = readUser != 0
		m.ReadByAdmin = readAdm != 0
		m.CreatedAt, _ = parseTime(created)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListSupportConversations returns one entry per open conversation, ordered by
// most recent activity first, for the admin support list.
func (s *Store) ListSupportConversations(ctx context.Context) ([]SupportConversation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.user_telegram_id,
		       (SELECT user_username FROM support_messages
		          WHERE user_telegram_id = c.user_telegram_id AND user_username != ''
		          ORDER BY id DESC LIMIT 1) AS username,
		       (SELECT text FROM support_messages
		          WHERE user_telegram_id = c.user_telegram_id
		          ORDER BY created_at DESC, id DESC LIMIT 1) AS last_text,
		       MAX(c.created_at) AS last_at,
		       SUM(CASE WHEN c.from_admin = 0 AND c.read_by_admin = 0 THEN 1 ELSE 0 END) AS unread
		FROM support_messages c
		GROUP BY c.user_telegram_id
		ORDER BY last_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SupportConversation
	for rows.Next() {
		var (
			conv     SupportConversation
			username sql.NullString
			lastText sql.NullString
			lastAt   string
		)
		if err := rows.Scan(&conv.UserTelegramID, &username, &lastText, &lastAt, &conv.Unread); err != nil {
			return nil, err
		}
		conv.UserUsername = username.String
		conv.LastText = lastText.String
		conv.LastAt, _ = parseTime(lastAt)
		out = append(out, conv)
	}
	return out, rows.Err()
}

// MarkSupportReadByUser marks every admin reply in a conversation as seen by
// the user.
func (s *Store) MarkSupportReadByUser(ctx context.Context, userTelegramID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE support_messages SET read_by_user = 1
		WHERE user_telegram_id = ? AND from_admin = 1 AND read_by_user = 0
	`, userTelegramID)
	return err
}

// MarkSupportReadByAdmin marks every user message in a conversation as seen by
// the admins.
func (s *Store) MarkSupportReadByAdmin(ctx context.Context, userTelegramID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE support_messages SET read_by_admin = 1
		WHERE user_telegram_id = ? AND from_admin = 0 AND read_by_admin = 0
	`, userTelegramID)
	return err
}

// CountUnreadForUser returns how many admin replies the user has not seen yet.
func (s *Store) CountUnreadForUser(ctx context.Context, userTelegramID int64) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM support_messages
		WHERE user_telegram_id = ? AND from_admin = 1 AND read_by_user = 0
	`, userTelegramID).Scan(&n)
	return n, err
}

// CloseSupportConversation deletes the whole thread. It reports whether any
// rows were removed (false = the conversation was already empty/closed).
func (s *Store) CloseSupportConversation(ctx context.Context, userTelegramID int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM support_messages WHERE user_telegram_id = ?`, userTelegramID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// CreateSupportTicket opens a new ticket for the user.
func (s *Store) CreateSupportTicket(ctx context.Context, userTelegramID int64, username, subject string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO support_tickets (user_telegram_id, user_username, subject, status, created_at)
		VALUES (?, ?, ?, 'open', ?)
	`, userTelegramID, username, subject, formatTime(time.Now()))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// GetSupportTicket returns one ticket by id, or nil when it does not exist.
func (s *Store) GetSupportTicket(ctx context.Context, id int64) (*SupportTicket, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_telegram_id, user_username, subject, status, created_at, closed_at, closed_by
		FROM support_tickets WHERE id = ?
	`, id)
	return scanSupportTicketBase(row)
}

// LatestOpenTicketForUser returns the user's newest open ticket, or nil when
// every ticket is closed (or none exist). This is the bot's ticket resolution:
// chat messages land in the newest open ticket.
func (s *Store) LatestOpenTicketForUser(ctx context.Context, userTelegramID int64) (*SupportTicket, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_telegram_id, user_username, subject, status, created_at, closed_at, closed_by
		FROM support_tickets
		WHERE user_telegram_id = ? AND status = 'open'
		ORDER BY id DESC LIMIT 1
	`, userTelegramID)
	return scanSupportTicketBase(row)
}

// ListSupportTicketsForUser returns the user's tickets — open first, then by
// last activity — with the last message summary and the user-side unread count
// (admin replies the user has not seen).
func (s *Store) ListSupportTicketsForUser(ctx context.Context, userTelegramID int64) ([]SupportTicket, error) {
	return s.listSupportTickets(ctx, ticketListQuery(
		`m.from_admin = 1 AND m.read_by_user = 0`,
		`WHERE t.user_telegram_id = ?`, 0), userTelegramID)
}

// ListSupportTicketsAdmin returns the admin inbox: every open ticket plus the
// 50 most recently active closed ones, open first, with the admin-side unread
// count (user messages the admins have not seen).
func (s *Store) ListSupportTicketsAdmin(ctx context.Context) ([]SupportTicket, error) {
	const unread = `m.from_admin = 0 AND m.read_by_admin = 0`
	open, err := s.listSupportTickets(ctx, ticketListQuery(unread, `WHERE t.status = 'open'`, 0))
	if err != nil {
		return nil, err
	}
	closed, err := s.listSupportTickets(ctx, ticketListQuery(unread, `WHERE t.status = 'closed'`, 50))
	if err != nil {
		return nil, err
	}
	return append(open, closed...), nil
}

// ticketListQuery builds the shared ticket-summary SELECT. unreadCond counts
// the side-dependent unread messages; where filters tickets; limit > 0 caps
// the rows. Activity ordering uses the last message id (monotonic) rather than
// second-granularity timestamps so it is deterministic.
func ticketListQuery(unreadCond, where string, limit int) string {
	q := `
		SELECT t.id, t.user_telegram_id, t.user_username, t.subject, t.status, t.created_at, t.closed_at, t.closed_by,
		       COALESCE((SELECT text FROM support_messages m WHERE m.ticket_id = t.id ORDER BY m.id DESC LIMIT 1), ''),
		       COALESCE((SELECT created_at FROM support_messages m WHERE m.ticket_id = t.id ORDER BY m.id DESC LIMIT 1), t.created_at),
		       COALESCE((SELECT from_admin FROM support_messages m WHERE m.ticket_id = t.id ORDER BY m.id DESC LIMIT 1), 0),
		       (SELECT COUNT(*) FROM support_messages m WHERE m.ticket_id = t.id AND ` + unreadCond + `)
		FROM support_tickets t
		` + where + `
		ORDER BY (t.status = 'open') DESC,
		         COALESCE((SELECT MAX(id) FROM support_messages m WHERE m.ticket_id = t.id), 0) DESC,
		         t.id DESC`
	if limit > 0 {
		q += fmt.Sprintf(` LIMIT %d`, limit)
	}
	return q
}

func (s *Store) listSupportTickets(ctx context.Context, query string, args ...any) ([]SupportTicket, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SupportTicket
	for rows.Next() {
		var (
			tk              SupportTicket
			created, lastAt string
			closedAt        sql.NullString
			lastFromAdmin   int
		)
		if err := rows.Scan(&tk.ID, &tk.UserTelegramID, &tk.UserUsername, &tk.Subject, &tk.Status,
			&created, &closedAt, &tk.ClosedBy, &tk.LastText, &lastAt, &lastFromAdmin, &tk.Unread); err != nil {
			return nil, err
		}
		tk.CreatedAt, _ = parseTime(created)
		tk.LastAt, _ = parseTime(lastAt)
		tk.LastFromAdmin = lastFromAdmin != 0
		if closedAt.Valid {
			if at, err := parseTime(closedAt.String); err == nil {
				tk.ClosedAt = &at
			}
		}
		out = append(out, tk)
	}
	return out, rows.Err()
}

func scanSupportTicketBase(row *sql.Row) (*SupportTicket, error) {
	var (
		tk       SupportTicket
		created  string
		closedAt sql.NullString
	)
	err := row.Scan(&tk.ID, &tk.UserTelegramID, &tk.UserUsername, &tk.Subject, &tk.Status,
		&created, &closedAt, &tk.ClosedBy)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	tk.CreatedAt, _ = parseTime(created)
	if closedAt.Valid {
		if at, err := parseTime(closedAt.String); err == nil {
			tk.ClosedAt = &at
		}
	}
	return &tk, nil
}

// CloseSupportTicket flips an open ticket to closed. The conditional UPDATE is
// the race guard: of two concurrent closes exactly one sees closed=true.
// Closed tickets never reopen; history stays readable.
func (s *Store) CloseSupportTicket(ctx context.Context, id int64, closedBy string, at time.Time) (bool, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE support_tickets SET status = 'closed', closed_at = ?, closed_by = ?
		WHERE id = ? AND status = 'open'
	`, formatTime(at), closedBy, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// MarkTicketReadByUser marks one ticket's admin replies as seen by the user.
func (s *Store) MarkTicketReadByUser(ctx context.Context, ticketID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE support_messages SET read_by_user = 1
		WHERE ticket_id = ? AND from_admin = 1 AND read_by_user = 0
	`, ticketID)
	return err
}

// MarkTicketReadByAdmin marks one ticket's user messages as seen by the admins.
func (s *Store) MarkTicketReadByAdmin(ctx context.Context, ticketID int64) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE support_messages SET read_by_admin = 1
		WHERE ticket_id = ? AND from_admin = 0 AND read_by_admin = 0
	`, ticketID)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
