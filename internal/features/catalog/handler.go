package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/a-h/templ"
)

type handler struct {
	store     *Store
	principal PrincipalResolver
}

func (h *handler) index(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.current(w, r)
	if !ok {
		return
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kind != "" && !validCatalogKind(kind) {
		kind = ""
	}
	overview, err := h.store.List(r.Context(), principal.TenantID, principal.UserID, CatalogFilter{
		Kind: kind, Query: strings.TrimSpace(r.URL.Query().Get("q")),
	})
	if err != nil {
		h.handleReadError(w, r, "list catalog", err)
		return
	}
	page := Index{
		Organization: overview.Organization, Now: time.Now(), Kind: kind,
		Query: strings.TrimSpace(r.URL.Query().Get("q")), Items: viewItems(overview.Items),
		Counts: overview.Counts, Locations: viewLocations(overview.Locations),
		CanManage: overview.CanManage, Notice: catalogNotice(r.URL.Query().Get("notice")),
	}
	h.render(w, r, List(page), http.StatusOK)
}

func (h *handler) newItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.current(w, r)
	if !ok {
		return
	}
	organization, canManage, locations, err := h.store.NewItemContext(r.Context(), principal.TenantID, principal.UserID)
	if err != nil {
		h.handleReadError(w, r, "load new catalog item", err)
		return
	}
	page := newCatalogForm(organization, canManage, locations)
	h.render(w, r, Form(page), http.StatusOK)
}

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.current(w, r)
	if !ok {
		return
	}
	values, input, fieldErrors := parseCatalogForm(r)
	organization, canManage, locations, err := h.store.NewItemContext(r.Context(), principal.TenantID, principal.UserID)
	if err != nil {
		h.handleReadError(w, r, "load catalog create form", err)
		return
	}
	page := catalogFormPage("", organization, canManage, true, locations, values)
	page.FieldErrors = fieldErrors
	if len(fieldErrors) != 0 {
		page.Notice = Notice{Kind: NoticeInvalid, Message: "Corrigez les champs signalés avant d’enregistrer."}
		h.render(w, r, Form(page), http.StatusUnprocessableEntity)
		return
	}
	created, err := h.store.Create(r.Context(), principal.TenantID, principal.UserID, input)
	if err != nil {
		h.handleItemWriteError(w, r, "create catalog item", err, page)
		return
	}
	http.Redirect(w, r, "/catalog/"+created.ID+"?notice=saved", http.StatusSeeOther)
}

func (h *handler) editItem(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.current(w, r)
	if !ok {
		return
	}
	item, organization, canManage, locations, err := h.store.Item(
		r.Context(), principal.TenantID, principal.UserID, r.PathValue("itemID"),
	)
	if err != nil {
		h.handleReadError(w, r, "load catalog item", err)
		return
	}
	page := catalogFormPage(item.ID, organization, canManage, item.SourceImportID == "", locations, itemFormValues(item))
	page.Notice = catalogNotice(r.URL.Query().Get("notice"))
	h.render(w, r, Form(page), http.StatusOK)
}

func (h *handler) update(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.current(w, r)
	if !ok {
		return
	}
	itemID := r.PathValue("itemID")
	values, input, fieldErrors := parseCatalogForm(r)
	current, organization, canManage, locations, err := h.store.Item(
		r.Context(), principal.TenantID, principal.UserID, itemID,
	)
	if err != nil {
		h.handleReadError(w, r, "load catalog update form", err)
		return
	}
	page := catalogFormPage(itemID, organization, canManage, current.SourceImportID == "", locations, values)
	page.FieldErrors = fieldErrors
	if len(fieldErrors) != 0 {
		page.Notice = Notice{Kind: NoticeInvalid, Message: "Corrigez les champs signalés avant d’enregistrer."}
		h.render(w, r, Form(page), http.StatusUnprocessableEntity)
		return
	}
	if _, err := h.store.Update(r.Context(), principal.TenantID, principal.UserID, itemID, input); err != nil {
		h.handleItemWriteError(w, r, "update catalog item", err, page)
		return
	}
	http.Redirect(w, r, "/catalog/"+itemID+"?notice=saved", http.StatusSeeOther)
}

func (h *handler) archive(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.current(w, r)
	if !ok {
		return
	}
	err := h.store.Archive(r.Context(), principal.TenantID, principal.UserID, r.PathValue("itemID"))
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, ErrForbidden) {
		http.Error(w, "Action interdite.", http.StatusForbidden)
		return
	}
	if err != nil {
		h.fail(w, "archive catalog item", err)
		return
	}
	http.Redirect(w, r, "/catalog?notice=archived", http.StatusSeeOther)
}

func (h *handler) imports(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.current(w, r)
	if !ok {
		return
	}
	overview, err := h.store.Imports(r.Context(), principal.TenantID, principal.UserID)
	if err != nil {
		h.handleReadError(w, r, "list catalog imports", err)
		return
	}
	page := Imports{
		Organization: overview.Organization, CanManage: overview.CanManage,
		Imports: viewImports(overview.Imports), Notice: catalogNotice(r.URL.Query().Get("notice")),
	}
	h.render(w, r, ImportList(page), http.StatusOK)
}

func (h *handler) newImport(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.current(w, r)
	if !ok {
		return
	}
	organization, canManage, locations, err := h.store.NewItemContext(r.Context(), principal.TenantID, principal.UserID)
	if err != nil {
		h.handleReadError(w, r, "load catalog upload", err)
		return
	}
	if !canManage {
		http.Error(w, "Action interdite.", http.StatusForbidden)
		return
	}
	page := Upload{
		Organization: organization, Locations: activeViewLocations(locations),
		Location: r.URL.Query().Get(FieldLocation), FieldErrors: make(map[string]string),
	}
	h.render(w, r, UploadForm(page), http.StatusOK)
}

func (h *handler) upload(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.current(w, r)
	if !ok {
		return
	}
	organization, canManage, locations, err := h.store.NewItemContext(r.Context(), principal.TenantID, principal.UserID)
	if err != nil {
		h.handleReadError(w, r, "load catalog upload", err)
		return
	}
	if !canManage {
		http.Error(w, "Action interdite.", http.StatusForbidden)
		return
	}
	page := Upload{Organization: organization, Locations: activeViewLocations(locations), FieldErrors: make(map[string]string)}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes+(1<<20))
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		page.FieldErrors[FieldFile] = "Le fichier est trop volumineux ou illisible."
		page.Notice = Notice{Kind: NoticeInvalid, Message: "Le fichier n’a pas pu être reçu."}
		h.render(w, r, UploadForm(page), http.StatusRequestEntityTooLarge)
		return
	}
	page.Location = strings.TrimSpace(r.FormValue(FieldLocation))
	file, header, err := r.FormFile(FieldFile)
	if err != nil {
		page.FieldErrors[FieldFile] = "Choisissez un fichier CSV ou XLSX."
	}
	if page.Location == "" {
		page.FieldErrors[FieldLocation] = "Choisissez le site concerné."
	}
	if len(page.FieldErrors) != 0 {
		if file != nil {
			if closeErr := file.Close(); closeErr != nil {
				slog.Error("close rejected catalog upload", "err", closeErr)
			}
		}
		page.Notice = Notice{Kind: NoticeInvalid, Message: "Complétez les champs signalés."}
		h.render(w, r, UploadForm(page), http.StatusUnprocessableEntity)
		return
	}
	content, readErr := io.ReadAll(io.LimitReader(file, maxUploadBytes+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		h.fail(w, "read catalog upload", err)
		return
	}
	record, err := h.store.Upload(r.Context(), principal.TenantID, principal.UserID, UploadInput{
		LocationID: page.Location, Filename: header.Filename,
		MediaType: header.Header.Get("Content-Type"), Content: content, Size: header.Size,
	})
	if errors.Is(err, ErrDuplicateImport) {
		http.Redirect(w, r, "/catalog/imports/"+record.ID+"?notice=duplicate", http.StatusSeeOther)
		return
	}
	if errors.Is(err, ErrForbidden) {
		http.Error(w, "Action interdite.", http.StatusForbidden)
		return
	}
	if err != nil {
		page.FieldErrors[FieldLocation] = "Ce site n’est pas disponible."
		page.Notice = Notice{Kind: NoticeInvalid, Message: "Vérifiez le site choisi."}
		h.render(w, r, UploadForm(page), http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, "/catalog/imports/"+record.ID, http.StatusSeeOther)
}

func (h *handler) importPreview(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.current(w, r)
	if !ok {
		return
	}
	preview, err := h.store.Import(r.Context(), principal.TenantID, principal.UserID, r.PathValue("importID"))
	if err != nil {
		h.handleReadError(w, r, "load catalog import", err)
		return
	}
	page := viewPreview(preview)
	mode := strings.TrimSpace(r.URL.Query().Get(FieldMode))
	if mode == ModeMerge || mode == ModeReplace {
		plan, planErr := h.store.Plan(r.Context(), principal.TenantID, principal.UserID, preview.Import.ID, mode)
		if planErr == nil {
			page.Mode = mode
			page.Plan = Plan{Create: plan.Create, Update: plan.Update, Remove: plan.Remove, Skip: plan.Skip}
		}
	}
	page.Notice = catalogNotice(r.URL.Query().Get("notice"))
	h.render(w, r, PreviewPage(page), http.StatusOK)
}

func (h *handler) publish(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.current(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Formulaire invalide.", http.StatusBadRequest)
		return
	}
	mode := strings.TrimSpace(r.FormValue(FieldMode))
	if (mode != ModeMerge && mode != ModeReplace) || r.FormValue(FieldConfirm) != "1" {
		http.Error(w, "Confirmez le mode de publication.", http.StatusUnprocessableEntity)
		return
	}
	importID := r.PathValue("importID")
	_, err := h.store.Publish(r.Context(), principal.TenantID, principal.UserID, importID, mode)
	if errors.Is(err, ErrForbidden) {
		http.Error(w, "Action interdite.", http.StatusForbidden)
		return
	}
	if errors.Is(err, ErrImportNotReady) || errors.Is(err, ErrImportEmpty) || errors.Is(err, ErrPublicationChanged) {
		http.Error(w, "Cet import ne peut plus être publié dans cet état.", http.StatusConflict)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.fail(w, "publish catalog import", err)
		return
	}
	http.Redirect(w, r, "/catalog/imports/"+importID+"?notice=published", http.StatusSeeOther)
}

func (h *handler) cancelImport(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.current(w, r)
	if !ok {
		return
	}
	err := h.store.Cancel(r.Context(), principal.TenantID, principal.UserID, r.PathValue("importID"))
	if errors.Is(err, ErrForbidden) {
		http.Error(w, "Action interdite.", http.StatusForbidden)
		return
	}
	if errors.Is(err, ErrImportNotReady) {
		http.Error(w, "Cet import n’est plus en attente.", http.StatusConflict)
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		h.fail(w, "cancel catalog import", err)
		return
	}
	http.Redirect(w, r, "/catalog/imports?notice=cancelled", http.StatusSeeOther)
}

func parseCatalogForm(r *http.Request) (FormValues, ItemInput, map[string]string) {
	values := FormValues{}
	errorsByField := make(map[string]string)
	if err := r.ParseForm(); err != nil {
		errorsByField[FieldName] = "Le formulaire est illisible."
		return values, ItemInput{}, errorsByField
	}
	values = FormValues{
		Kind: strings.TrimSpace(r.FormValue(FieldKind)), Name: strings.TrimSpace(r.FormValue(FieldName)),
		Reference: strings.TrimSpace(r.FormValue(FieldReference)), Description: strings.TrimSpace(r.FormValue(FieldDescription)),
		PriceKind: strings.TrimSpace(r.FormValue(FieldPriceKind)), Amount: strings.TrimSpace(r.FormValue(FieldAmount)),
		MaxAmount: strings.TrimSpace(r.FormValue(FieldMaxAmount)), TaxBasis: strings.TrimSpace(r.FormValue(FieldTaxBasis)),
		VATRate: strings.TrimSpace(r.FormValue(FieldVATRate)), Duration: strings.TrimSpace(r.FormValue(FieldDuration)),
		From: strings.TrimSpace(r.FormValue(FieldFrom)), To: strings.TrimSpace(r.FormValue(FieldTo)),
		Scope: strings.TrimSpace(r.FormValue(FieldScope)), LocationIDs: uniqueStrings(r.Form[FieldLocations]),
	}
	input := ItemInput{
		Kind: values.Kind, Name: values.Name, Reference: values.Reference,
		Description: values.Description, PriceKind: values.PriceKind,
		TaxBasis: values.TaxBasis, LocationScope: values.Scope, LocationIDs: values.LocationIDs,
	}
	if !validCatalogKind(values.Kind) {
		errorsByField[FieldKind] = "Choisissez un type de ligne."
	}
	if values.Name == "" {
		errorsByField[FieldName] = "Saisissez un libellé."
	} else if utf8.RuneCountInString(values.Name) > 160 {
		errorsByField[FieldName] = "Le libellé ne peut pas dépasser 160 caractères."
	}
	if utf8.RuneCountInString(values.Reference) > 80 {
		errorsByField[FieldReference] = "La référence ne peut pas dépasser 80 caractères."
	}
	if utf8.RuneCountInString(values.Description) > 2000 {
		errorsByField[FieldDescription] = "La description ne peut pas dépasser 2 000 caractères."
	}
	if values.PriceKind != PriceFixed && values.PriceKind != PriceFrom && values.PriceKind != PriceRange && values.PriceKind != PriceQuote {
		errorsByField[FieldPriceKind] = "Choisissez comment annoncer le prix."
	} else if values.PriceKind == PriceQuote {
		if values.Amount != "" {
			errorsByField[FieldAmount] = "Un prix sur devis ne contient pas de montant."
		}
		if values.MaxAmount != "" {
			errorsByField[FieldMaxAmount] = "Un prix sur devis ne contient pas de maximum."
		}
	} else {
		amount, err := parseMoney(values.Amount)
		if err != nil {
			errorsByField[FieldAmount] = "Saisissez un montant positif avec au plus deux décimales."
		} else {
			input.AmountCents = &amount
		}
		if values.PriceKind == PriceRange {
			maximum, err := parseMoney(values.MaxAmount)
			if err != nil {
				errorsByField[FieldMaxAmount] = "Saisissez le haut de la fourchette."
			} else {
				input.MaxAmountCents = &maximum
				if input.AmountCents != nil && maximum < *input.AmountCents {
					errorsByField[FieldMaxAmount] = "Le maximum doit être supérieur ou égal au premier montant."
				}
			}
		} else if values.MaxAmount != "" {
			errorsByField[FieldMaxAmount] = "Ce type de prix ne prend pas de montant maximum."
		}
	}
	if values.TaxBasis != TaxExclusive && values.TaxBasis != TaxInclusive {
		errorsByField[FieldTaxBasis] = "Précisez si le prix est HT ou TTC."
	}
	vat, err := parseRate(values.VATRate)
	if err != nil {
		errorsByField[FieldVATRate] = "Choisissez un taux de TVA."
	} else {
		input.VATBasisPoints = vat
	}
	if values.Duration != "" {
		duration, err := parseDuration(values.Duration)
		if err != nil || (values.Kind != KindService && values.Kind != KindPackage) {
			errorsByField[FieldDuration] = "La durée doit être comprise entre 5 et 1 440 minutes pour une prestation ou un forfait."
		} else {
			input.DurationMinutes = &duration
		}
	}
	input.EffectiveFrom, err = parseOptionalDate(values.From)
	if err != nil {
		errorsByField[FieldFrom] = "Saisissez une date valide."
	}
	input.EffectiveTo, err = parseOptionalDate(values.To)
	if err != nil {
		errorsByField[FieldTo] = "Saisissez une date valide."
	}
	if input.EffectiveFrom != nil && input.EffectiveTo != nil && input.EffectiveFrom.After(*input.EffectiveTo) {
		errorsByField[FieldTo] = "La fin doit être postérieure ou égale au début."
	}
	if values.Scope == ScopeAll {
		input.LocationIDs = nil
	} else if values.Scope != ScopeSelected {
		errorsByField[FieldScope] = "Choisissez où ce prix s’applique."
	} else if len(values.LocationIDs) == 0 {
		errorsByField[FieldLocations] = "Choisissez au moins un site."
	}
	return values, input, errorsByField
}

func newCatalogForm(organization string, canManage bool, locations []CatalogLocation) FormPage {
	return catalogFormPage("", organization, canManage, true, locations, FormValues{
		Kind: KindService, PriceKind: PriceFixed, TaxBasis: TaxInclusive,
		VATRate: "20", Scope: ScopeAll,
	})
}

func catalogFormPage(id, organization string, canManage, deletable bool, locations []CatalogLocation, values FormValues) FormPage {
	return FormPage{
		ID: id, Organization: organization, Values: values, CanManage: canManage,
		Deletable: deletable, Locations: viewLocations(locations), FieldErrors: make(map[string]string),
		VATRates: []Option{{Value: "0", Label: "0 %"}, {Value: "5.5", Label: "5,5 %"}, {Value: "10", Label: "10 %"}, {Value: "20", Label: "20 %"}},
	}
}

func itemFormValues(item CatalogItemRecord) FormValues {
	values := FormValues{
		Kind: item.Kind, Name: item.Name, Reference: item.Reference, Description: item.Description,
		PriceKind: item.PriceKind, TaxBasis: item.TaxBasis,
		VATRate: formatRateInput(item.VATBasisPoints), Scope: item.LocationScope,
		LocationIDs: item.LocationIDs,
	}
	if item.AmountCents != nil {
		values.Amount = formatDecimal(int(*item.AmountCents), 100)
	}
	if item.MaxAmountCents != nil {
		values.MaxAmount = formatDecimal(int(*item.MaxAmountCents), 100)
	}
	if item.DurationMinutes != nil {
		values.Duration = strconv.Itoa(*item.DurationMinutes)
	}
	if item.EffectiveFrom != nil {
		values.From = item.EffectiveFrom.Format("2006-01-02")
	}
	if item.EffectiveTo != nil {
		values.To = item.EffectiveTo.Format("2006-01-02")
	}
	return values
}

func viewItems(records []CatalogItemRecord) []Item {
	items := make([]Item, 0, len(records))
	for _, record := range records {
		price := Price{Kind: record.PriceKind, TaxBasis: record.TaxBasis, VATRate: record.VATBasisPoints, PerHour: record.PerHour, Currency: record.Currency}
		if record.AmountCents != nil {
			price.Cents = *record.AmountCents
		}
		if record.MaxAmountCents != nil {
			price.MaxCents = *record.MaxAmountCents
		}
		item := Item{ID: record.ID, Kind: record.Kind, Name: record.Name, Reference: record.Reference, Description: record.Description, Price: price, LocationNames: record.LocationNames}
		if record.DurationMinutes != nil {
			item.Duration = *record.DurationMinutes
		}
		if record.EffectiveFrom != nil {
			item.EffectiveFrom = *record.EffectiveFrom
		}
		if record.EffectiveTo != nil {
			item.EffectiveTo = *record.EffectiveTo
		}
		items = append(items, item)
	}
	return items
}

func viewLocations(records []CatalogLocation) []LocationRef {
	locations := make([]LocationRef, 0, len(records))
	for _, record := range records {
		locations = append(locations, LocationRef{ID: record.ID, Name: record.Name, Active: record.Active})
	}
	return locations
}

func activeViewLocations(records []CatalogLocation) []LocationRef {
	locations := make([]CatalogLocation, 0, len(records))
	for _, record := range records {
		if record.Active {
			locations = append(locations, record)
		}
	}
	return viewLocations(locations)
}

func viewImports(records []ImportRecord) []Import {
	imports := make([]Import, 0, len(records))
	for _, record := range records {
		imports = append(imports, viewImport(record))
	}
	return imports
}

func viewImport(record ImportRecord) Import {
	return Import{ID: record.ID, Filename: record.Filename, LocationName: record.LocationName, UploadedBy: record.UploadedBy, UploadedAt: record.UploadedAt, Status: record.Status, Mode: record.Mode, Valid: record.ValidRows, Ambiguous: record.AmbiguousRows, Rejected: record.RejectedRows, Reason: record.Rejection}
}

func viewPreview(record ImportPreview) Preview {
	page := Preview{Organization: record.Organization, Import: viewImport(record.Import), CanManage: record.CanManage}
	for _, source := range record.Rows {
		price := Price{Kind: source.PriceKind, TaxBasis: source.TaxBasis, VATRate: source.VATBasisPoints, Currency: "EUR", PerHour: source.Kind == KindLabourRate}
		if source.AmountCents != nil {
			price.Cents = *source.AmountCents
		}
		if source.MaxAmountCents != nil {
			price.MaxCents = *source.MaxAmountCents
		}
		page.Rows = append(page.Rows, Row{Number: source.Number, Status: source.Classification, Name: source.Name, Kind: source.Kind, Price: price, Issue: source.Issue, Collides: source.MatchingName})
	}
	return page
}

func validCatalogKind(kind string) bool {
	for _, candidate := range Kinds {
		if kind == candidate {
			return true
		}
	}
	return false
}

func formatDecimal(value, scale int) string {
	whole, fraction := value/scale, value%scale
	if fraction == 0 {
		return strconv.Itoa(whole)
	}
	return fmt.Sprintf("%d.%02d", whole, fraction)
}

func formatRateInput(basisPoints int) string {
	whole, fraction := basisPoints/100, basisPoints%100
	if fraction == 0 {
		return strconv.Itoa(whole)
	}
	if fraction%10 == 0 {
		return fmt.Sprintf("%d.%d", whole, fraction/10)
	}
	return fmt.Sprintf("%d.%02d", whole, fraction)
}

func catalogNotice(code string) Notice {
	switch code {
	case "saved":
		return Notice{Kind: NoticeSuccess, Message: "La ligne du catalogue est enregistrée."}
	case "archived":
		return Notice{Kind: NoticeSuccess, Message: "La ligne a été retirée du catalogue publié."}
	case "published":
		return Notice{Kind: NoticeSuccess, Message: "Les prix validés sont maintenant publiés."}
	case "cancelled":
		return Notice{Kind: NoticeSuccess, Message: "L’import a été abandonné sans modifier le catalogue."}
	case "duplicate":
		return Notice{Kind: NoticeDuplicate, Message: "Ce fichier a déjà été analysé pour ce site."}
	}
	return Notice{}
}

func (h *handler) handleItemWriteError(w http.ResponseWriter, r *http.Request, operation string, err error, page FormPage) {
	switch {
	case errors.Is(err, ErrForbidden):
		http.Error(w, "Action interdite.", http.StatusForbidden)
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	case errors.Is(err, ErrDuplicateReference):
		page.FieldErrors[FieldReference] = "Cette référence est déjà utilisée."
		page.Notice = Notice{Kind: NoticeDuplicate, Message: "Utilisez une autre référence ou modifiez la ligne existante."}
		h.render(w, r, Form(page), http.StatusConflict)
	case errors.Is(err, ErrInvalidLocation):
		page.FieldErrors[FieldLocations] = "Un des sites choisis n’est plus disponible."
		page.Notice = Notice{Kind: NoticeInvalid, Message: "Actualisez les sites concernés."}
		h.render(w, r, Form(page), http.StatusUnprocessableEntity)
	default:
		h.fail(w, operation, err)
	}
}

func (h *handler) current(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	principal, ok := h.principal(r.Context())
	if !ok || principal.UserID == "" || principal.TenantID == "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return Principal{}, false
	}
	return principal, true
}

func (h *handler) handleReadError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if errors.Is(err, ErrForbidden) {
		http.Error(w, "Action interdite.", http.StatusForbidden)
		return
	}
	h.fail(w, operation, err)
}

func (h *handler) fail(w http.ResponseWriter, operation string, err error) {
	slog.Error(operation, "err", err)
	http.Error(w, "Impossible de charger le catalogue.", http.StatusInternalServerError)
}

func (h *handler) render(w http.ResponseWriter, r *http.Request, component templ.Component, status int) {
	w.WriteHeader(status)
	if err := component.Render(r.Context(), w); err != nil {
		slog.Error("render catalog", "err", err)
	}
}

type Middleware func(http.Handler) http.Handler

type Principal struct{ UserID, TenantID string }

type PrincipalResolver func(context.Context) (Principal, bool)
