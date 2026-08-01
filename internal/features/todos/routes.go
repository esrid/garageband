package todos

import "net/http"

// Register mounts the feature's routes on the root mux — the feature owns its
// URLs (Django's urls.py equivalent).
func Register(mux *http.ServeMux, store *Store) {
	h := &handler{store: store}
	mux.HandleFunc("GET /todos", h.list)
	mux.HandleFunc("POST /todos", h.create)
	mux.HandleFunc("POST /todos/{id}/toggle", h.toggle)
	mux.HandleFunc("POST /todos/{id}/delete", h.delete)
	mux.HandleFunc("GET /api/todos", h.listJSON)
}
