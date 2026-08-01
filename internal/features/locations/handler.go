package locations

import (
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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

func (h *handler) showSchedule(w http.ResponseWriter, r *http.Request) {
	principal, locationID, ok := h.locationRequest(w, r)
	if !ok {
		return
	}
	page, err := h.schedulePage(r, principal, locationID)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	switch r.URL.Query().Get("saved") {
	case "hours-added":
		page.Notice = Notice{Kind: NoticeSuccess, Message: "La plage d’ouverture a été ajoutée."}
	case "hours-deleted":
		page.Notice = Notice{Kind: NoticeSuccess, Message: "La plage d’ouverture a été retirée."}
	case "closure-added":
		page.Notice = Notice{Kind: NoticeSuccess, Message: "La fermeture exceptionnelle a été ajoutée."}
	case "closure-deleted":
		page.Notice = Notice{Kind: NoticeSuccess, Message: "La fermeture exceptionnelle a été retirée."}
	}
	h.renderSchedule(w, r, page, http.StatusOK)
}

func (h *handler) addOpeningHour(w http.ResponseWriter, r *http.Request) {
	principal, locationID, ok := h.locationRequest(w, r)
	if !ok {
		return
	}
	input, fieldErrors := parseOpeningHour(r)
	if len(fieldErrors) != 0 {
		h.renderScheduleRetry(w, r, principal, locationID, input, ClosureInput{}, fieldErrors)
		return
	}
	if err := h.store.AddOpeningHour(r.Context(), principal.TenantID, principal.UserID, locationID, input); err != nil {
		h.scheduleWriteError(w, r, principal, locationID, input, ClosureInput{}, FieldOpensAt, err)
		return
	}
	h.scheduleRedirect(w, r, locationID, "hours-added")
}

func (h *handler) deleteOpeningHour(w http.ResponseWriter, r *http.Request) {
	principal, locationID, ok := h.locationRequest(w, r)
	if !ok {
		return
	}
	input, fieldErrors := parseOpeningHour(r)
	if len(fieldErrors) != 0 {
		http.Error(w, "Plage horaire invalide.", http.StatusBadRequest)
		return
	}
	if err := h.store.DeleteOpeningHour(r.Context(), principal.TenantID, principal.UserID, locationID, input); err != nil {
		h.scheduleStoreError(w, r, err)
		return
	}
	h.scheduleRedirect(w, r, locationID, "hours-deleted")
}

func (h *handler) addClosure(w http.ResponseWriter, r *http.Request) {
	principal, locationID, ok := h.locationRequest(w, r)
	if !ok {
		return
	}
	input, fieldErrors := parseClosure(r)
	if len(fieldErrors) != 0 {
		h.renderScheduleRetry(w, r, principal, locationID, OpeningHourInput{Weekday: 1, OpensAt: "08:00", ClosesAt: "18:00"}, input, fieldErrors)
		return
	}
	if _, err := h.store.AddClosure(r.Context(), principal.TenantID, principal.UserID, locationID, input); err != nil {
		h.scheduleWriteError(w, r, principal, locationID, OpeningHourInput{Weekday: 1, OpensAt: "08:00", ClosesAt: "18:00"}, input, FieldClosureStartDate, err)
		return
	}
	h.scheduleRedirect(w, r, locationID, "closure-added")
}

func (h *handler) deleteClosure(w http.ResponseWriter, r *http.Request) {
	principal, locationID, ok := h.locationRequest(w, r)
	if !ok {
		return
	}
	closureID := r.PathValue("closureID")
	if !uuidPattern.MatchString(closureID) {
		http.NotFound(w, r)
		return
	}
	if err := h.store.DeleteClosure(r.Context(), principal.TenantID, principal.UserID, locationID, closureID); err != nil {
		h.scheduleStoreError(w, r, err)
		return
	}
	h.scheduleRedirect(w, r, locationID, "closure-deleted")
}

func (h *handler) schedulePage(r *http.Request, principal Principal, locationID string) (SchedulePage, error) {
	schedule, err := h.store.Schedule(r.Context(), principal.TenantID, principal.UserID, locationID)
	if err != nil {
		return SchedulePage{}, err
	}
	now := time.Now()
	if zone, err := time.LoadLocation(schedule.Location.Timezone); err == nil {
		now = now.In(zone)
	}
	return SchedulePage{
		Organization: schedule.Organization, Location: schedule.Location,
		Enabled: schedule.Enabled, OpeningHours: schedule.OpeningHours,
		Closures: schedule.Closures, CanManage: schedule.CanManage,
		HourValues: OpeningHourInput{Weekday: 1, OpensAt: "08:00", ClosesAt: "18:00"},
		ClosureValues: ClosureInput{
			StartsDate: now.Format(DateLayout), StartsTime: "12:00",
			EndsDate: now.Format(DateLayout), EndsTime: "14:00",
		},
		FieldErrors: map[string]string{},
	}, nil
}

func parseOpeningHour(r *http.Request) (OpeningHourInput, map[string]string) {
	input := OpeningHourInput{}
	fieldErrors := make(map[string]string)
	if err := r.ParseForm(); err != nil {
		fieldErrors[FieldWeekday] = "Le formulaire envoyé est invalide."
		return input, fieldErrors
	}
	weekday, err := strconv.Atoi(strings.TrimSpace(r.FormValue(FieldWeekday)))
	if err != nil || weekday < 0 || weekday > 6 {
		fieldErrors[FieldWeekday] = "Choisissez un jour de la semaine."
	}
	input = OpeningHourInput{
		Weekday:  weekday,
		OpensAt:  strings.TrimSpace(r.FormValue(FieldOpensAt)),
		ClosesAt: strings.TrimSpace(r.FormValue(FieldClosesAt)),
	}
	opens, opensErr := time.Parse("15:04", input.OpensAt)
	closes, closesErr := time.Parse("15:04", input.ClosesAt)
	if opensErr != nil {
		fieldErrors[FieldOpensAt] = "Choisissez une heure d’ouverture valide."
	}
	if closesErr != nil {
		fieldErrors[FieldClosesAt] = "Choisissez une heure de fermeture valide."
	} else if opensErr == nil && !closes.After(opens) {
		fieldErrors[FieldClosesAt] = "La fermeture doit être après l’ouverture."
	}
	return input, fieldErrors
}

func parseClosure(r *http.Request) (ClosureInput, map[string]string) {
	input := ClosureInput{}
	fieldErrors := make(map[string]string)
	if err := r.ParseForm(); err != nil {
		fieldErrors[FieldClosureStartDate] = "Le formulaire envoyé est invalide."
		return input, fieldErrors
	}
	input = ClosureInput{
		StartsDate: strings.TrimSpace(r.FormValue(FieldClosureStartDate)),
		StartsTime: strings.TrimSpace(r.FormValue(FieldClosureStartTime)),
		EndsDate:   strings.TrimSpace(r.FormValue(FieldClosureEndDate)),
		EndsTime:   strings.TrimSpace(r.FormValue(FieldClosureEndTime)),
		Reason:     strings.TrimSpace(r.FormValue(FieldClosureReason)),
	}
	const layout = DateLayout + " 15:04"
	startsAt, startsErr := time.Parse(layout, input.StartsDate+" "+input.StartsTime)
	endsAt, endsErr := time.Parse(layout, input.EndsDate+" "+input.EndsTime)
	if startsErr != nil {
		fieldErrors[FieldClosureStartDate] = "Choisissez une date et une heure de début valides."
	}
	if endsErr != nil {
		fieldErrors[FieldClosureEndDate] = "Choisissez une date et une heure de fin valides."
	} else if startsErr == nil && !endsAt.After(startsAt) {
		fieldErrors[FieldClosureEndTime] = "La fin doit être après le début."
	}
	if utf8.RuneCountInString(input.Reason) > 300 {
		fieldErrors[FieldClosureReason] = "Le motif ne peut pas dépasser 300 caractères."
	}
	return input, fieldErrors
}

func (h *handler) renderScheduleRetry(
	w http.ResponseWriter,
	r *http.Request,
	principal Principal,
	locationID string,
	hour OpeningHourInput,
	closure ClosureInput,
	fieldErrors map[string]string,
) {
	page, err := h.schedulePage(r, principal, locationID)
	if err != nil {
		h.storeError(w, r, err)
		return
	}
	if hour.OpensAt != "" || hour.ClosesAt != "" {
		page.HourValues = hour
	}
	if closure.StartsDate != "" || closure.EndsDate != "" {
		page.ClosureValues = closure
	}
	page.FieldErrors = fieldErrors
	page.Notice = Notice{Kind: NoticeInvalid, Message: "Corrigez les champs indiqués avant de continuer."}
	h.renderSchedule(w, r, page, http.StatusUnprocessableEntity)
}

func (h *handler) scheduleWriteError(
	w http.ResponseWriter,
	r *http.Request,
	principal Principal,
	locationID string,
	hour OpeningHourInput,
	closure ClosureInput,
	conflictField string,
	err error,
) {
	if errors.Is(err, ErrForbidden) || errors.Is(err, sql.ErrNoRows) {
		h.scheduleStoreError(w, r, err)
		return
	}
	page, loadErr := h.schedulePage(r, principal, locationID)
	if loadErr != nil {
		h.storeError(w, r, loadErr)
		return
	}
	if hour.OpensAt != "" || hour.ClosesAt != "" {
		page.HourValues = hour
	}
	if closure.StartsDate != "" || closure.EndsDate != "" {
		page.ClosureValues = closure
	}
	page.FieldErrors = map[string]string{}
	status := http.StatusInternalServerError
	var fieldError *ScheduleFieldError
	var postgresError *pgconn.PgError
	switch {
	case errors.As(err, &fieldError):
		page.FieldErrors[fieldError.Field] = fieldError.Message
		page.Notice = Notice{Kind: NoticeInvalid, Message: "Corrigez les champs indiqués avant de continuer."}
		status = http.StatusUnprocessableEntity
	case errors.As(err, &postgresError) && postgresError.Code == "23P01":
		page.FieldErrors[conflictField] = "Cette période chevauche une période déjà enregistrée."
		page.Notice = Notice{Kind: NoticeInvalid, Message: "Les périodes d’un même type ne peuvent pas se chevaucher."}
		status = http.StatusConflict
	case errors.As(err, &postgresError) && postgresError.ConstraintName == "closures_avoid_appointments":
		page.FieldErrors[conflictField] = "Un rendez-vous actif existe pendant cette fermeture."
		page.Notice = Notice{Kind: NoticeInvalid, Message: "Déplacez ou annulez le rendez-vous avant de fermer l’atelier."}
		status = http.StatusConflict
	default:
		slog.Error("write location schedule", "err", err)
		page.Notice = Notice{Kind: NoticeError, Message: "Le planning n’a pas pu être enregistré. Réessayez."}
	}
	h.renderSchedule(w, r, page, status)
}

func (h *handler) scheduleStoreError(w http.ResponseWriter, r *http.Request, err error) {
	var postgresError *pgconn.PgError
	switch {
	case errors.Is(err, ErrForbidden):
		http.Error(w, "Vous n’avez pas les droits nécessaires pour modifier ce planning.", http.StatusForbidden)
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	case errors.As(err, &postgresError) && postgresError.ConstraintName == "opening_hours_preserve_appointments":
		http.Error(w, "Déplacez ou annulez les rendez-vous concernés avant de retirer cette plage.", http.StatusConflict)
	default:
		slog.Error("write location schedule", "err", err)
		http.Error(w, "Impossible de modifier le planning.", http.StatusInternalServerError)
	}
}

func (h *handler) scheduleRedirect(w http.ResponseWriter, r *http.Request, locationID string, result string) {
	http.Redirect(w, r, "/locations/"+locationID+"/schedule?saved="+result, http.StatusSeeOther)
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
	if errors.As(err, &postgresError) && postgresError.ConstraintName == "locations_timezone_preserves_schedule" {
		page.FieldErrors = map[string]string{
			FieldTimezone: "Ce fuseau ne peut plus changer après la création d’un rendez-vous ou d’une fermeture.",
		}
		page.Notice = Notice{
			Kind: NoticeInvalid, Message: "Le fuseau protège les heures déjà enregistrées.",
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

func (h *handler) renderSchedule(
	w http.ResponseWriter,
	r *http.Request,
	page SchedulePage,
	status int,
) {
	w.WriteHeader(status)
	if err := ScheduleView(page).Render(r.Context(), w); err != nil {
		slog.Error("render location schedule", "err", err)
	}
}
