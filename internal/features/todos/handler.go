package todos

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"
)

type handler struct{ store *Store }

func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.List(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	if err := Page(items).Render(r.Context(), w); err != nil {
		slog.Error("render todos page", "err", err)
	}
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" || utf8.RuneCountInString(title) > 200 {
		http.Error(w, "title must be 1-200 characters", http.StatusBadRequest)
		return
	}
	if _, err := h.store.Create(r.Context(), title); err != nil {
		h.fail(w, err)
		return
	}
	http.Redirect(w, r, "/todos", http.StatusSeeOther)
}

func (h *handler) toggle(w http.ResponseWriter, r *http.Request) {
	h.mutate(w, r, h.store.Toggle)
}

func (h *handler) delete(w http.ResponseWriter, r *http.Request) {
	h.mutate(w, r, h.store.Delete)
}

func (h *handler) listJSON(w http.ResponseWriter, r *http.Request) {
	items, err := h.store.List(r.Context())
	if err != nil {
		h.fail(w, err)
		return
	}
	if items == nil {
		items = []Todo{}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(items); err != nil {
		slog.Error("encode todos json", "err", err)
	}
}

func (h *handler) mutate(w http.ResponseWriter, r *http.Request, op func(ctx context.Context, id string) error) {
	err := op(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	case err != nil:
		h.fail(w, err)
	default:
		http.Redirect(w, r, "/todos", http.StatusSeeOther)
	}
}

func (h *handler) fail(w http.ResponseWriter, err error) {
	slog.Error("todos", "err", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}
