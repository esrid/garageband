package locations

import (
	"fmt"
	"strconv"
	"strings"
)

// View models for the locations screens. They deliberately build on the store
// types in queries.go — Location for reading, Input for writing — so a handler
// hands rows straight to the templates with no mapping layer, and the form
// edits exactly what the store accepts.

// StatusActive is the status a site must have to take calls and appointments.
const StatusActive = "active"

// Form field keys. They are also the input `name` attributes, so these
// constants are the single source of truth for what a handler must parse and
// for the keys used in FieldErrors.
const (
	FieldName                 = "name"
	FieldSIRET                = "siret"
	FieldAddressLine1         = "address_line1"
	FieldAddressLine2         = "address_line2"
	FieldPostalCode           = "postal_code"
	FieldCity                 = "city"
	FieldCountry              = "country_code"
	FieldTimezone             = "timezone"
	FieldEmail                = "email"
	FieldPhone                = "phone"
	FieldWebsite              = "website"
	FieldWeekday              = "weekday"
	FieldOpensAt              = "opens_at"
	FieldClosesAt             = "closes_at"
	FieldClosureStartDate     = "closure_start_date"
	FieldClosureStartTime     = "closure_start_time"
	FieldClosureEndDate       = "closure_end_date"
	FieldClosureEndTime       = "closure_end_time"
	FieldClosureReason        = "closure_reason"
	FieldResourceName         = "resource_name"
	FieldResourceKind         = "resource_kind"
	FieldResourceActive       = "resource_active"
	FieldRequirementService   = "requirement_service_id"
	FieldRequirementKind      = "requirement_kind"
	FieldRequirementQuantity  = "requirement_quantity"
	FieldCatalogItem          = "catalog_item_id"
	FieldCatalogServiceActive = "catalog_service_active"
)

// Notice kinds. The view derives the heading from the kind, so French copy
// stays in the view layer instead of leaking into handlers.
const (
	NoticeError   = "error"   // the store or an upstream service failed
	NoticeInvalid = "invalid" // the submitted form needs corrections
	NoticeSuccess = "success" // the last action went through
)

// Notice is a single server-side outcome to show at the top of a screen.
type Notice struct {
	Kind    string
	Message string
}

func (n Notice) Empty() bool { return strings.TrimSpace(n.Message) == "" }

// IndexPage backs the "Sites du garage" screen.
type IndexPage struct {
	Organization string     // active workspace name, so the scope is obvious
	Locations    []Location // store rows, already ordered by the query
	CanManage    bool       // false renders the read-only presentation
	Notice       Notice
}

func (p IndexPage) HasInactive() bool {
	for _, location := range p.Locations {
		if !isActive(location) {
			return true
		}
	}
	return false
}

// FormPage backs the add and edit screens; they are the same form.
type FormPage struct {
	ID          string // empty when adding a site
	Active      bool
	Values      Input             // exactly what Store.Create and Store.Update take
	FieldErrors map[string]string // field key -> French message, shown inline
	Notice      Notice
	CanManage   bool

	// CalendarOffered is false when the Google Calendar feature itself is
	// off (no OAuth client or encryption key configured), in which case the
	// section does not render at all - matching how login providers stay
	// hidden when unconfigured.
	CalendarOffered bool
	// CalendarConnected and CalendarAccount are independent: a connection
	// can exist with an empty account (the display-only email lookup at
	// connect time failed) - never infer "connected" from the account
	// string being non-empty.
	CalendarConnected bool
	CalendarAccount   string
}

type SchedulePage struct {
	Organization      string
	Location          Location
	Enabled           bool
	OpeningHours      []OpeningHour
	Closures          []Closure
	Resources         []WorkshopResource
	Services          []SchedulingService
	CatalogItems      []CatalogSchedulingItem
	CanManage         bool
	HourValues        OpeningHourInput
	ClosureValues     ClosureInput
	ResourceValues    ResourceInput
	RequirementValues RequirementInput
	CatalogItemValue  string
	FieldErrors       map[string]string
	Notice            Notice
}

func (p SchedulePage) Error(field string) string { return p.FieldErrors[field] }

func (p SchedulePage) HasError(field string) bool { return p.FieldErrors[field] != "" }

func (p SchedulePage) HoursFor(weekday int) []OpeningHour {
	var hours []OpeningHour
	for _, opening := range p.OpeningHours {
		if opening.Weekday == weekday {
			hours = append(hours, opening)
		}
	}
	return hours
}

func (p FormPage) IsNew() bool { return strings.TrimSpace(p.ID) == "" }

func (p FormPage) Error(field string) string { return p.FieldErrors[field] }

func (p FormPage) HasError(field string) bool { return p.FieldErrors[field] != "" }

func isActive(location Location) bool { return location.Status == StatusActive }

// addressLines renders the address as the successive lines of a postal label,
// skipping whatever is not filled in.
func addressLines(location Location) []string {
	var lines []string
	for _, line := range []string{location.AddressLine1, location.AddressLine2} {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if city := strings.TrimSpace(location.PostalCode + " " + location.City); city != "" {
		lines = append(lines, city)
	}
	return lines
}

// Setup reports how ready a site is to take calls and appointments.
type Setup struct {
	Total   int
	Missing []string
}

func (s Setup) Done() int {
	done := s.Total - len(s.Missing)
	if done < 0 {
		return 0
	}
	return done
}

func (s Setup) Complete() bool { return s.Total > 0 && len(s.Missing) == 0 }

// setupOf is the view's definition of a usable site: the agent needs somewhere
// to send customers, a number to quote, a mailbox, and a clock to book against.
func setupOf(location Location) Setup {
	setup := Setup{Total: 5}
	for _, check := range []struct{ field, value string }{
		{FieldAddressLine1, location.AddressLine1},
		{FieldSIRET, location.SIRET},
		{FieldPhone, location.PhoneE164},
		{FieldEmail, location.Email},
		{FieldTimezone, location.Timezone},
	} {
		if strings.TrimSpace(check.value) == "" {
			setup.Missing = append(setup.Missing, check.field)
		}
	}
	return setup
}

// Option is one entry of a select control.
type Option struct {
	Value string
	Label string
}

// Timezones a French garage can realistically operate in, mainland and
// overseas. IANA identifiers, not invented values.
var timezoneOptions = []Option{
	{"Europe/Paris", "Europe/Paris (métropole)"},
	{"Indian/Reunion", "Indian/Reunion (La Réunion)"},
	{"America/Martinique", "America/Martinique (Martinique)"},
	{"America/Guadeloupe", "America/Guadeloupe (Guadeloupe)"},
	{"America/Cayenne", "America/Cayenne (Guyane)"},
	{"Indian/Mayotte", "Indian/Mayotte (Mayotte)"},
	{"Pacific/Noumea", "Pacific/Noumea (Nouvelle-Calédonie)"},
}

// Countries reachable by road from a French garage's customer base.
var countryOptions = []Option{
	{"FR", "France"},
	{"BE", "Belgique"},
	{"CH", "Suisse"},
	{"LU", "Luxembourg"},
	{"MC", "Monaco"},
	{"DE", "Allemagne"},
	{"ES", "Espagne"},
	{"IT", "Italie"},
}

var weekdayOptions = []Option{
	{"1", "Lundi"},
	{"2", "Mardi"},
	{"3", "Mercredi"},
	{"4", "Jeudi"},
	{"5", "Vendredi"},
	{"6", "Samedi"},
	{"0", "Dimanche"},
}

var resourceKindOptions = []Option{
	{"technician", "Technicien"},
	{"bay", "Pont ou baie"},
	{"equipment", "Équipement"},
	{"calendar", "Calendrier"},
}

func resourceKindLabel(kind string) string {
	for _, option := range resourceKindOptions {
		if option.Value == kind {
			return option.Label
		}
	}
	return kind
}

func resourceStatusLabel(active bool) string {
	if active {
		return "Actif"
	}
	return "Inactif"
}

func resourceActionLabel(active bool) string {
	if active {
		return "Désactiver"
	}
	return "Réactiver"
}

func schedulingStatusLabel(active bool) string {
	if active {
		return "Réservable"
	}
	return "Non réservable"
}

func schedulingActionLabel(active bool) string {
	if active {
		return "Désactiver"
	}
	return "Activer"
}

func catalogPriceLabel(price CatalogPrice) string {
	var amount string
	switch price.Kind {
	case "fixed":
		amount = centsLabel(price.AmountCents)
	case "from":
		amount = "À partir de " + centsLabel(price.AmountCents)
	case "range":
		amount = centsLabel(price.AmountCents) + " à " + centsLabel(price.MaxAmountCents)
	case "quote":
		return "Sur devis"
	default:
		return "Prix du catalogue indisponible"
	}
	if price.TaxBasis == "excl" {
		return amount + " HT"
	}
	return amount + " TTC"
}

func centsLabel(cents int64) string {
	return fmt.Sprintf("%d,%02d €", cents/100, cents%100)
}

func weekdayNumber(option Option) int {
	weekday, _ := strconv.Atoi(option.Value)
	return weekday
}

func closurePeriod(closure Closure) string {
	if closure.StartsAt.Format(DateLayout) == closure.EndsAt.Format(DateLayout) {
		return fmt.Sprintf(
			"%s, %s–%s",
			closure.StartsAt.Format("02/01/2006"),
			closure.StartsAt.Format("15:04"),
			closure.EndsAt.Format("15:04"),
		)
	}
	return fmt.Sprintf(
		"Du %s à %s au %s à %s",
		closure.StartsAt.Format("02/01/2006"),
		closure.StartsAt.Format("15:04"),
		closure.EndsAt.Format("02/01/2006"),
		closure.EndsAt.Format("15:04"),
	)
}

const DateLayout = "2006-01-02"

// fieldLabel is the French name of a field, used by the setup meter to say what
// is still missing.
func fieldLabel(key string) string {
	switch key {
	case FieldName:
		return "nom du site"
	case FieldSIRET:
		return "SIRET"
	case FieldAddressLine1:
		return "adresse"
	case FieldAddressLine2:
		return "complément d'adresse"
	case FieldPostalCode:
		return "code postal"
	case FieldCity:
		return "ville"
	case FieldCountry:
		return "pays"
	case FieldTimezone:
		return "fuseau horaire"
	case FieldEmail:
		return "e-mail"
	case FieldPhone:
		return "téléphone"
	case FieldWebsite:
		return "site web"
	default:
		return key
	}
}

// missingLabels turns field keys into a readable French list.
func missingLabels(missing []string) string {
	labels := make([]string, 0, len(missing))
	for _, key := range missing {
		labels = append(labels, fieldLabel(key))
	}
	return strings.Join(labels, ", ")
}

func noticeTitle(kind string) string {
	switch kind {
	case NoticeError:
		return "Action impossible pour le moment"
	case NoticeSuccess:
		return "C'est enregistré"
	default:
		return "Vérifiez les informations ci-dessous"
	}
}

func noticeColor(kind string) string {
	switch kind {
	case NoticeError:
		return "alert-warning"
	case NoticeSuccess:
		return "alert-success"
	default:
		return "alert-error"
	}
}

// formTitle names the screen; the same form both adds and edits.
func formTitle(p FormPage) string {
	if p.IsNew() {
		return "Ajouter un site"
	}
	return "Configurer le site"
}

// formActionPath is where the form posts. Creating targets the collection,
// editing targets the site itself.
func formActionPath(p FormPage) string {
	if p.IsNew() {
		return "/locations"
	}
	return "/locations/" + p.ID
}

// setupCount is the readable half of the progress bar, so the state never
// depends on the bar alone.
func setupCount(s Setup) string {
	if s.Complete() {
		return "Complète"
	}
	return strconv.Itoa(s.Done()) + " / " + strconv.Itoa(s.Total)
}

func setupValue(s Setup) string { return strconv.Itoa(s.Done()) }

// setupMax never returns 0: a progress element with max="0" renders undefined.
func setupMax(s Setup) string {
	if s.Total < 1 {
		return "1"
	}
	return strconv.Itoa(s.Total)
}

// ariaInvalid renders the attribute value; "false" is the ARIA-defined way to
// say a control is currently valid.
func ariaInvalid(hasError bool) string {
	if hasError {
		return "true"
	}
	return "false"
}

// describedBy links a control to its hint and, when present, its error message,
// so screen readers announce the problem with the field.
func describedBy(field string, hasHint bool, hasError bool) string {
	ids := make([]string, 0, 2)
	if hasHint {
		ids = append(ids, field+"-hint")
	}
	if hasError {
		ids = append(ids, field+"-error")
	}
	return strings.Join(ids, " ")
}

// metaFacts are the secondary identifiers of a site, already labelled inline so
// the card needs no column of bold terms. Missing values are skipped: the setup
// meter is what reports them.
func metaFacts(location Location) []string {
	var facts []string
	if location.SIRET != "" {
		facts = append(facts, "SIRET "+location.SIRET)
	}
	if location.PhoneE164 != "" {
		facts = append(facts, "Tél. "+location.PhoneE164)
	}
	if location.Timezone != "" {
		facts = append(facts, location.Timezone)
	}
	return facts
}
