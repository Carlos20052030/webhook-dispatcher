package store

import "database/sql"

// Store owns runtime queries. Migrations are package-level: they run
// once at boot and do not belong to the request-scoped object.
type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}
