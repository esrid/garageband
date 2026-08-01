package db

import (
	"database/sql"
	"errors"
	"testing"
)

func TestRebind(t *testing.T) {
	pg := &DB{dialect: "postgres"}
	lite := &DB{dialect: "sqlite"}

	q := `INSERT INTO t (a, b, c) VALUES (?, ?, ?)`
	if got, want := pg.R(q), `INSERT INTO t (a, b, c) VALUES ($1, $2, $3)`; got != want {
		t.Errorf("postgres: got %q, want %q", got, want)
	}
	if got := lite.R(q); got != q {
		t.Errorf("sqlite: got %q, want unchanged", got)
	}
	if got := pg.R(`SELECT 1`); got != `SELECT 1` {
		t.Errorf("no placeholders: got %q", got)
	}
}

func TestExecOne(t *testing.T) {
	d, err := Open("file:" + t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := d.Exec(`CREATE TABLE t (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := d.ExecOne(t.Context(), `INSERT INTO t (id) VALUES (?)`, "a"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := d.ExecOne(t.Context(), `DELETE FROM t WHERE id = ?`, "missing"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown id: got %v, want sql.ErrNoRows", err)
	}
}
