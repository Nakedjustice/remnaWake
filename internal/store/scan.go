package store

import "database/sql"

// rowScanner matches the Scan signature shared by *sql.Row and *sql.Rows so
// one per-row scan function serves both single-row and list queries.
type rowScanner interface{ Scan(...any) error }

// scanList drains rows into a slice using the given per-row scan function.
func scanList[T any](rows *sql.Rows, scan func(rowScanner) (T, error)) ([]T, error) {
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
