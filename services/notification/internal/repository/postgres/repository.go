package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository implements repository.NotificationRepository backed by PostgreSQL.
// Methods are split across:
//   - notifications.go — notification CRUD (write, read, mark-read)
//   - outbox.go        — transactional outbox lifecycle (claim, publish, release)
type Repository struct {
	db *pgxpool.Pool
}

// NewRepository creates a new PostgreSQL-backed repository.
func NewRepository(db *pgxpool.Pool) *Repository {
	return &Repository{db: db}
}
