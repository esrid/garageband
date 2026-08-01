package todos

import (
	"context"
	"crypto/rand"
	"time"

	"github.com/esrid/garageband/internal/platform/db"
)

type Todo struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Done      bool      `json:"done"`
	CreatedAt time.Time `json:"created_at"`
}

// Store holds the feature's SQL (Django's models.py equivalent, without ORM).
// Queries are written with ? placeholders; db.R rewrites them for PostgreSQL.
type Store struct{ db *db.DB }

func NewStore(d *db.DB) *Store { return &Store{db: d} }

func (s *Store) List(ctx context.Context) (out []Todo, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, title, done, created_at FROM todos ORDER BY created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cerr := rows.Close(); cerr != nil && err == nil {
			out, err = nil, cerr
		}
	}()

	for rows.Next() {
		var t Todo
		var created string
		if err := rows.Scan(&t.ID, &t.Title, &t.Done, &created); err != nil {
			return nil, err
		}
		if t.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Create(ctx context.Context, title string) (Todo, error) {
	t := Todo{ID: rand.Text(), Title: title, CreatedAt: time.Now().UTC()}
	_, err := s.db.ExecContext(ctx,
		s.db.R(`INSERT INTO todos (id, title, done, created_at) VALUES (?, ?, ?, ?)`),
		t.ID, t.Title, t.Done, t.CreatedAt.Format(time.RFC3339Nano))
	return t, err
}

// Toggle and Delete return sql.ErrNoRows for an unknown id (via db.ExecOne).

func (s *Store) Toggle(ctx context.Context, id string) error {
	return s.db.ExecOne(ctx, `UPDATE todos SET done = NOT done WHERE id = ?`, id)
}

func (s *Store) Delete(ctx context.Context, id string) error {
	return s.db.ExecOne(ctx, `DELETE FROM todos WHERE id = ?`, id)
}
