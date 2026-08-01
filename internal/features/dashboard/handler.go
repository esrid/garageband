package dashboard

import (
	"log/slog"
	"net/http"
)

type handler struct {
	user       UserResolver
	workspaces WorkspaceLister
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	user, ok := h.user(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	workspaces, err := h.workspaces(r.Context(), user.ID)
	if err != nil {
		slog.Error("list dashboard workspaces", "err", err)
		http.Error(w, "Unable to load workspaces.", http.StatusInternalServerError)
		return
	}
	data := PageData{
		Onboarded:  r.URL.Query().Get("onboarded") == "1",
		Workspaces: workspaces,
	}
	for index := range workspaces {
		if workspaces[index].ID == user.ActiveTenantID {
			data.Active = &workspaces[index]
			break
		}
	}
	if err := Page(data).Render(r.Context(), w); err != nil {
		slog.Error("render dashboard", "err", err)
	}
}
