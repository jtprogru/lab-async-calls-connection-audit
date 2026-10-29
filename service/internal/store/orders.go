package store

import "database/sql"

// Status открывает пул к базе на каждый вызов.
func Status(dsn, id string) (string, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return "", err
	}
	defer db.Close()
	var status string
	err = db.QueryRow("SELECT status FROM orders WHERE id = $1", id).Scan(&status)
	return status, err
}
