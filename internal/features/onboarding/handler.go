package onboarding

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode"

	"github.com/esrid/garageband/internal/platform/businesslookup"
)

type handler struct {
	store          *Store
	provider       businesslookup.Provider
	userID         UserIDResolver
	activateTenant TenantActivator
}

type formData struct {
	DraftID      string
	Name         string
	LegalName    string
	Slug         string
	WebsiteURL   string
	LocationName string
	SIRET        string
	AddressLine1 string
	AddressLine2 string
	PostalCode   string
	City         string
	CountryCode  string
	Error        string
	ErrorKind    string // one of the notice* constants; drives the alert styling
}

func (h *handler) show(w http.ResponseWriter, r *http.Request) {
	h.renderLookup(w, r, "", http.StatusOK)
}

func (h *handler) lookup(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		h.renderLookup(w, r, "Envoi du formulaire invalide.", http.StatusBadRequest)
		return
	}
	siret := strings.TrimSpace(r.FormValue("siret"))
	if !validSIRET(siret) {
		h.renderLookup(w, r, "Saisissez un SIRET de 14 chiffres exactement.", http.StatusUnprocessableEntity)
		return
	}
	profile, err := h.provider.Enrich(r.Context(), businesslookup.Request{SIRET: siret})
	if err != nil {
		switch {
		case errors.Is(err, businesslookup.ErrNotFound):
			h.renderLookup(w, r, "Aucun établissement actif ne correspond à ce SIRET.", http.StatusUnprocessableEntity)
			return
		case errors.Is(err, businesslookup.ErrInactive):
			h.renderLookup(w, r, "Cet établissement est fermé dans le registre officiel.", http.StatusUnprocessableEntity)
			return
		}
		slog.Error("look up SIRET", "err", err)
		h.renderLookup(w, r, "Le registre officiel est momentanément indisponible. Réessayez.", http.StatusBadGateway)
		return
	}
	userID, ok := h.userID(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	draft, err := h.store.CreateDraft(r.Context(), userID, h.provider, profile)
	if err != nil {
		slog.Error("create onboarding draft", "err", err)
		http.Error(w, "Impossible d'enregistrer le brouillon de configuration.", http.StatusInternalServerError)
		return
	}
	data := formFromProfile(draft.ID, profile)
	h.renderConfirmation(w, r, data, http.StatusOK)
}

func (h *handler) confirm(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Envoi du formulaire invalide.", http.StatusBadRequest)
		return
	}
	data := formFromRequest(r)
	if message := validateConfirmation(data); message != "" {
		data.Error, data.ErrorKind = message, noticeInvalid
		h.renderConfirmation(w, r, data, http.StatusUnprocessableEntity)
		return
	}
	userID, ok := h.userID(r.Context())
	if !ok {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	tenantID, err := h.store.FinalizeDraft(r.Context(), userID, data.DraftID, GarageInput{
		Name: data.Name, LegalName: data.LegalName, Slug: data.Slug,
		WebsiteURL: data.WebsiteURL, LocationName: data.LocationName,
		SIRET: data.SIRET, AddressLine1: data.AddressLine1,
		AddressLine2: data.AddressLine2, PostalCode: data.PostalCode,
		City: data.City, CountryCode: data.CountryCode,
	})
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.Error(w, "Brouillon de configuration introuvable.", http.StatusNotFound)
		case errors.Is(err, ErrDraftExpired):
			data.Error, data.ErrorKind = "Votre recherche est trop ancienne pour être utilisée. Relancez la recherche du SIRET, cela ne prend qu'un instant.", noticeExpired
			h.renderConfirmation(w, r, data, http.StatusConflict)
		case errors.Is(err, ErrInvalidGarage):
			data.Error, data.ErrorKind = "Les informations du garage sont invalides.", noticeInvalid
			h.renderConfirmation(w, r, data, http.StatusUnprocessableEntity)
		case errors.Is(err, ErrSIRETMismatch):
			data.Error, data.ErrorKind = "Pour utiliser un autre SIRET, lancez une nouvelle recherche.", noticeMismatch
			h.renderConfirmation(w, r, data, http.StatusConflict)
		default:
			if message, ok := duplicateMessage(err); ok {
				data.Error, data.ErrorKind = message, noticeDuplicate
				h.renderConfirmation(w, r, data, http.StatusConflict)
				return
			}
			slog.Error("finalize onboarding", "err", err)
			data.Error, data.ErrorKind = "Le garage n'a pas pu être créé. Vérifiez les informations et réessayez.", noticeInvalid
			h.renderConfirmation(w, r, data, http.StatusConflict)
		}
		return
	}
	if err := h.activateTenant(r.Context(), tenantID); err != nil {
		slog.Error("activate onboarded tenant", "tenant_id", tenantID, "err", err)
		data.Error, data.ErrorKind = "Votre garage a bien été créé, mais l'espace n'a pas pu être ouvert. Renvoyez le formulaire pour réessayer.", noticeUnavailable
		h.renderConfirmation(w, r, data, http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/?onboarded=1", http.StatusSeeOther)
}

func (h *handler) renderLookup(w http.ResponseWriter, r *http.Request, message string, status int) {
	w.WriteHeader(status)
	siret := strings.TrimSpace(r.FormValue("siret"))
	page := LookupPage(siret, message, noticeKindForLookupStatus(status))
	if err := page.Render(r.Context(), w); err != nil {
		slog.Error("render onboarding lookup", "err", err)
	}
}

func (h *handler) renderConfirmation(w http.ResponseWriter, r *http.Request, data formData, status int) {
	w.WriteHeader(status)
	if err := ConfirmationPage(data).Render(r.Context(), w); err != nil {
		slog.Error("render onboarding confirmation", "err", err)
	}
}

func formFromProfile(draftID string, profile businesslookup.Profile) formData {
	name := firstNonEmpty(profile.TradingName, profile.LegalName)
	return formData{
		DraftID: draftID, Name: name, LegalName: profile.LegalName,
		Slug: slugify(name), LocationName: name, SIRET: profile.SIRET,
		WebsiteURL: profile.WebsiteURL, AddressLine1: profile.Address.Line1,
		AddressLine2: profile.Address.Line2, PostalCode: profile.Address.PostalCode,
		City: profile.Address.City, CountryCode: firstNonEmpty(profile.Address.CountryCode, "FR"),
	}
}

func formFromRequest(r *http.Request) formData {
	return formData{
		DraftID:      strings.TrimSpace(r.FormValue("draft_id")),
		Name:         strings.TrimSpace(r.FormValue("name")),
		LegalName:    strings.TrimSpace(r.FormValue("legal_name")),
		Slug:         strings.TrimSpace(r.FormValue("slug")),
		WebsiteURL:   strings.TrimSpace(r.FormValue("website_url")),
		LocationName: strings.TrimSpace(r.FormValue("location_name")),
		SIRET:        strings.TrimSpace(r.FormValue("siret")),
		AddressLine1: strings.TrimSpace(r.FormValue("address_line1")),
		AddressLine2: strings.TrimSpace(r.FormValue("address_line2")),
		PostalCode:   strings.TrimSpace(r.FormValue("postal_code")),
		City:         strings.TrimSpace(r.FormValue("city")),
		CountryCode:  strings.ToUpper(strings.TrimSpace(r.FormValue("country_code"))),
	}
}

func validateConfirmation(data formData) string {
	if data.DraftID == "" || data.Name == "" || data.LocationName == "" {
		return "Le nom du garage, le nom du site et le brouillon sont obligatoires."
	}
	if !validSIRET(data.SIRET) {
		return "Le SIRET doit contenir exactement 14 chiffres."
	}
	if data.Slug != slugify(data.Slug) || len(data.Slug) < 2 || len(data.Slug) > 63 {
		return "L'identifiant d'espace doit comporter 2 à 63 lettres minuscules, chiffres ou tirets."
	}
	if len(data.CountryCode) != 2 {
		return "Le code pays doit contenir deux lettres."
	}
	if data.WebsiteURL != "" && !strings.HasPrefix(data.WebsiteURL, "https://") && !strings.HasPrefix(data.WebsiteURL, "http://") {
		return "Le site web doit commencer par https:// ou http://."
	}
	return ""
}

func validSIRET(value string) bool {
	if len(value) != 14 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func slugify(value string) string {
	var result strings.Builder
	lastHyphen := false
	for _, character := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			result.WriteRune(character)
			lastHyphen = false
		case unicode.IsSpace(character) || character == '-' || character == '_':
			if result.Len() > 0 && !lastHyphen {
				result.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(result.String(), "-")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
