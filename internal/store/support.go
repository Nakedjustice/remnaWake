package store

import (
	"context"
	"database/sql"
	"time"
)

// SupportMessage is one line in a user<->admin support conversation. A
// conversation is identified by UserTelegramID (the user who owns it); the set
// of rows sharing that id is the thread, and "closing" the chat deletes them.
type SupportMessage struct {
	ID               int64
	UserTelegramID   int64
	UserUsername     string
	FromAdmin        bool  // false = user wrote, true = admin wrote
	AuthorTelegramID int64 // which admin replied (0 for user messages)
	Text             string
	CreatedAt        time.Time
	ReadByUser       bool
	ReadByAdmin      bool
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
			(user_telegram_id, user_username, from_admin, author_telegram_id, text, created_at, read_by_user, read_by_admin)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, m.UserTelegramID, m.UserUsername, boolToInt(m.FromAdmin), m.AuthorTelegramID,
		m.Text, formatTime(time.Now()), boolToInt(!m.FromAdmin), boolToInt(m.FromAdmin))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListSupportMessages returns a conversation's messages in chronological order.
func (s *Store) ListSupportMessages(ctx context.Context, userTelegramID int64) ([]SupportMessage, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_telegram_id, user_username, from_admin, author_telegram_id, text, created_at, read_by_user, read_by_admin
		FROM support_messages
		WHERE user_telegram_id = ?
		ORDER BY created_at ASC, id ASC
	`, userTelegramID)
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
		if err := rows.Scan(&m.ID, &m.UserTelegramID, &m.UserUsername, &fromAdmin,
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

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
