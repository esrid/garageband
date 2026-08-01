package locations

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

var (
	uuidPattern    = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	siretPattern   = regexp.MustCompile(`^[0-9]{14}$`)
	phonePattern   = regexp.MustCompile(`^\+[1-9][0-9]{7,14}$`)
	emailPattern   = regexp.MustCompile(`^[^\s@]+@[^\s@]+$`)
	countryPattern = regexp.MustCompile(`^[A-Za-z]{2}$`)
)

type handler struct {
	store     *Store
	principal PrincipalResolver
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	overview, err := h.store.Overview(
		r.Context(), principal.TenantID, principal.UserID,
	)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	notice := Notice{}
	switch r.URL.Query().Get("saved") {
	case "created":
		notice = Notice{Kind: NoticeSuccess, Message: "Le nouveau site a été ajouté."}
	case "updated":
		notice = Notice{Kind: NoticeSuccess, Message: "Les informations du site ont été enregistrées."}
	case "deactivated":
		notice = Notice{Kind: NoticeSuccess, Message: "Le site a été désactivé sans supprimer son historique."}
	case "reactivated":
		notice = Notice{Kind: NoticeSuccess, Message: "Le site est de nouveau actif."}
	}
	h.renderIndex(w, r, IndexPage{
		Organization: overview.Organization,
		Locations:    overview.Locations,
		CanManage:    overview.CanManage,
		Notice:       notice,
	}, http.StatusOK)
}

func (h *handler) showNew(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	overview, err := h.store.Overview(
		r.Context(), principal.TenantID, principal.UserID,
	)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	h.renderForm(w, r, FormPage{
		CanManage: overview.CanManage,
		Values: Input{
			CountryCode: "FR",
			Timezone:    "Europe/Paris",
		},
	}, http.StatusOK)
}

func (h *handler) showEdit(w http.ResponseWriter, r *http.Request) {
	principal, locationID, ok := h.locationRequest(w, r)
	if !ok {
		return
	}
	location, canManage, err := h.store.Get(
		r.Context(), principal.TenantID, principal.UserID, locationID,
	)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	h.renderForm(w, r, formFromLocation(location, canManage), http.StatusOK)
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return
	}
	input, fieldErrors, ok := parseInput(r)
	if !ok {
		h.renderForm(w, r, FormPage{
			Values:      input,
			FieldErrors: fieldErrors,
			Notice: Notice{
				Kind: NoticeInvalid, Message: "Corrigez les champs indiqués avant de continuer.",
			},
			CanManage: true,
		}, http.StatusUnprocessableEntity)
		return
	}
	if _, err := h.store.Create(
		r.Context(), principal.TenantID, principal.UserID, input,
	); err != nil {
		h.writeError(w, r, FormPage{Values: input, CanManage: true}, err)
		return
	}
	http.Redirect(w, r, "/locations?saved=created", http.StatusSeeOther)
}

func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	principal, locationID, ok := h.locationRequest(w, r)
	if !ok {
		return
	}
	input, fieldErrors, valid := parseInput(r)
	page := FormPage{
		ID: locationID, Active: true, Values: input, FieldErrors: fieldErrors,
		CanManage: true,
	}
	if !valid {
		page.Notice = Notice{
			Kind: NoticeInvalid, Message: "Corrigez les champs indiqués avant de continuer.",
		}
		h.renderForm(w, r, page, http.StatusUnprocessableEntity)
		return
	}
	location, err := h.store.Update(
		r.Context(), principal.TenantID, principal.UserID, locationID, input,
	)
	if err != nil {
		h.writeError(w, r, page, err)
		return
	}
	page.Active = location.Status == StatusActive
	http.Redirect(w, r, "/locations?saved=updated", http.StatusSeeOther)
}

func (h *handler) deactivate(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, "inactive", "deactivated")
}

func (h *handler) reactivate(w http.ResponseWriter, r *http.Request) {
	h.setStatus(w, r, StatusActive, "reactivated")
}

func (h *handler) setStatus(
	w http.ResponseWriter,
	r *http.Request,
	status string,
	result string,
) {
	principal, locationID, ok := h.locationRequest(w, r)
	if !ok {
		return
	}
	if _, err := h.store.SetStatus(
		r.Context(), principal.TenantID, principal.UserID, locationID, status,
	); err != nil {
		h.storeError(w, r, err)
		return
	}
	http.Redirect(w, r, "/locations?saved="+result, http.StatusSeeOther)
}

func (h *handler) resolve(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	principal, ok := h.principal(r.Context())
	if !ok || principal.UserID == "" || principal.TenantID == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return Principal{}, false
	}
	return principal, true
}

func (h *handler) locationRequest(
	w http.ResponseWriter,
	r *http.Request,
) (Principal, string, bool) {
	principal, ok := h.resolve(w, r)
	if !ok {
		return Principal{}, "", false
	}
	locationID := r.PathValue("locationID")
	if !uuidPattern.MatchString(locationID) {
		http.NotFound(w, r)
		return Principal{}, "", false
	}
	return principal, locationID, true
}

func parseInput(r *http.Request) (Input, map[string]string, bool) {
	input := Input{}
	fieldErrors := make(map[string]string)
	if err := r.ParseForm(); err != nil {
		fieldErrors[FieldName] = "Le formulaire envoyé est invalide."
		return input, fieldErrors, false
	}
	input = Input{
		Name:         strings.TrimSpace(r.FormValue(FieldName)),
		SIRET:        strings.TrimSpace(r.FormValue(FieldSIRET)),
		AddressLine1: strings.TrimSpace(r.FormValue(FieldAddressLine1)),
		AddressLine2: strings.TrimSpace(r.FormValue(FieldAddressLine2)),
		PostalCode:   strings.TrimSpace(r.FormValue(FieldPostalCode)),
		City:         strings.TrimSpace(r.FormValue(FieldCity)),
		CountryCode:  strings.ToUpper(strings.TrimSpace(r.FormValue(FieldCountry))),
		Timezone:     strings.TrimSpace(r.FormValue(FieldTimezone)),
		Email:        strings.ToLower(strings.TrimSpace(r.FormValue(FieldEmail))),
		PhoneE164:    strings.TrimSpace(r.FormValue(FieldPhone)),
		WebsiteURL:   strings.TrimSpace(r.FormValue(FieldWebsite)),
	}
	if input.Name == "" {
		fieldErrors[FieldName] = "Donnez un nom à ce site pour le reconnaître."
	}
	if input.SIRET != "" && !siretPattern.MatchString(input.SIRET) {
		fieldErrors[FieldSIRET] = "Le SIRET doit contenir exactement 14 chiffres."
	}
	if !countryPattern.MatchString(input.CountryCode) {
		fieldErrors[FieldCountry] = "Utilisez un code pays composé de deux lettres."
	}
	if !knownTimezone(input.Timezone) {
		fieldErrors[FieldTimezone] = "Choisissez un fuseau horaire proposé dans la liste."
	}
	if input.Email != "" && !emailPattern.MatchString(input.Email) {
		fieldErrors[FieldEmail] = "Saisissez une adresse e-mail valide."
	}
	if input.PhoneE164 != "" && !phonePattern.MatchString(input.PhoneE164) {
		fieldErrors[FieldPhone] = "Utilisez le format international, par exemple +596596123456."
	}
	if input.WebsiteURL != "" && !validWebsite(input.WebsiteURL) {
		fieldErrors[FieldWebsite] = "Saisissez une adresse commençant par https:// ou http://."
	}
	return input, fieldErrors, len(fieldErrors) == 0
}

func knownTimezone(value string) bool {
	for _, option := range timezoneOptions {
		if option.Value == value {
			return true
		}
	}
	return false
}

func validWebsite(value string) bool {
	parsed, err := url.ParseRequestURI(value)
	return err == nil && parsed.Host != "" &&
		(parsed.Scheme == "https" || parsed.Scheme == "http")
}

func formFromLocation(location Location, canManage bool) FormPage {
	return FormPage{
		ID: location.ID, Active: location.Status == StatusActive,
		CanManage: canManage,
		Values: Input{
			Name: location.Name, SIRET: location.SIRET,
			PhoneE164: location.PhoneE164, Email: location.Email,
			WebsiteURL: location.WebsiteURL, AddressLine1: location.AddressLine1,
			AddressLine2: location.AddressLine2, PostalCode: location.PostalCode,
			City: location.City, CountryCode: location.CountryCode,
			Timezone: location.Timezone,
		},
	}
}

func (h *handler) writeError(w http.ResponseWriter, r *http.Request, page FormPage, err error) {
	if errors.Is(err, ErrForbidden) {
		http.Error(w, "Vous n'avez pas les droits nécessaires pour modifier les sites.", http.StatusForbidden)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.ConstraintName == "locations_siret_unique" {
		page.FieldErrors = map[string]string{
			FieldSIRET: "Ce SIRET est déjà utilisé par un autre site.",
		}
		page.Notice = Notice{
			Kind: NoticeInvalid, Message: "Ce site existe peut-être déjà.",
		}
		h.renderForm(w, r, page, http.StatusConflict)
		return
	}
	slog.Error("write location", "err", err)
	page.Notice = Notice{
		Kind: NoticeError, Message: "Le site n'a pas pu être enregistré. Réessayez.",
	}
	h.renderForm(w, r, page, http.StatusInternalServerError)
}

func (h *handler) storeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		http.Error(w, "Accès interdit.", http.StatusForbidden)
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	default:
		slog.Error("read location", "err", err)
		http.Error(w, "Impossible de charger les sites.", http.StatusInternalServerError)
	}
}

func (h *handler) renderIndex(
	w http.ResponseWriter,
	r *http.Request,
	page IndexPage,
	status int,
) {
	w.WriteHeader(status)
	if err := Index(page).Render(r.Context(), w); err != nil {
		slog.Error("render locations index", "err", err)
	}
}

func (h *handler) renderForm(
	w http.ResponseWriter,
	r *http.Request,
	page FormPage,
	status int,
) {
	w.WriteHeader(status)
	if err := Form(page).Render(r.Context(), w); err != nil {
		slog.Error("render location form", "err", err)
	}
}
