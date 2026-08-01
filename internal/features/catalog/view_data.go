// Package catalog renders the priced offering of an organization — what the
// telephone agent is allowed to quote — and the staged imports that fill it.
//
// It owns no data and talks to no database: a handler builds these view models
// from the store. The contract is written down in docs/catalog-ui-contract.md.
// The models are local on purpose — a feature never imports another feature.
//
// Every derived state takes the current time as an argument instead of calling
// time.Now(): rendering stays a pure function of its input, so a test can put a
// price on either side of its effective dates without waiting for a clock.
package catalog

import (
	"strconv"
	"strings"
	"time"
)

// Item kinds, as constrained by catalog_items.kind.
const (
	KindService    = "service"
	KindProduct    = "product"
	KindPackage    = "package"
	KindSupplement = "supplement"
	KindLabourRate = "labour_rate"
)

// Kinds lists the item kinds in the order the screens show them.
var Kinds = []string{KindService, KindProduct, KindPackage, KindSupplement, KindLabourRate}

// How a price is expressed. A garage rarely has one number: bodywork starts at
// a price, a diagnosis lands in a bracket, and a gearbox is quoted after
// inspection. The agent has to say the right one.
const (
	PriceFixed = "fixed"
	PriceFrom  = "from"
	PriceRange = "range"
	PriceQuote = "quote"
)

// Whether the amount includes VAT. A garage quoting a private customer speaks
// TTC and one quoting a fleet speaks HT; getting this wrong misquotes by 20%.
const (
	TaxExclusive = "excl" // HT
	TaxInclusive = "incl" // TTC
)

// Availability of an item relative to a moment, derived from its effective
// dates. The agent may only quote an active one.
const (
	AvailabilityActive    = "active"
	AvailabilityScheduled = "scheduled"
	AvailabilityExpired   = "expired"
)

// Whether an item applies everywhere or to named sites only. The scope is
// explicit rather than inferred from an empty list, so "no site selected" is a
// validation error instead of a silent "available everywhere".
const (
	ScopeAll      = "all"
	ScopeSelected = "selected"
)

// Import lifecycle, as constrained by catalog_imports.status.
const (
	ImportAnalyzing = "analyzing" // file accepted, rows not classified yet
	ImportReady     = "ready"     // classified, waiting for a human decision
	ImportPublished = "published"
	ImportRejected  = "rejected" // the file itself could not be used
	ImportCancelled = "cancelled"
)

// How an import meets the existing catalog. The choice is never defaulted: one
// of these adds, the other deletes.
const (
	ModeMerge   = "merge"
	ModeReplace = "replace"
)

// Classification of a single imported row.
const (
	RowValid     = "valid"
	RowAmbiguous = "ambiguous" // usable, but it collides with something
	RowRejected  = "rejected"  // unusable, never imported
)

// Why a whole file was refused.
const (
	RejectUnsupported = "unsupported"
	RejectTooLarge    = "too_large"
	RejectEmpty       = "empty"
	RejectUnreadable  = "unreadable"
	RejectNoColumns   = "no_columns"
)

// MaxUploadBytes is what the screens promise; the handler is what enforces it.
const MaxUploadBytes = 5 << 20

// Form field names, which are also what the handler parses.
const (
	FieldKind        = "kind"
	FieldName        = "name"
	FieldReference   = "reference"
	FieldDescription = "description"
	FieldPriceKind   = "price_kind"
	FieldAmount      = "amount"
	FieldMaxAmount   = "max_amount"
	FieldTaxBasis    = "tax_basis"
	FieldVATRate     = "vat_rate"
	FieldDuration    = "duration_minutes"
	FieldFrom        = "effective_from"
	FieldTo          = "effective_to"
	FieldScope       = "location_scope"
	FieldLocations   = "location_ids"

	FieldFile     = "file"
	FieldLocation = "location_id"
	FieldMode     = "mode"
	FieldConfirm  = "confirm"
)

// Notice kinds. The view derives the heading from the kind, so French copy
// stays in the view layer instead of leaking into handlers.
const (
	NoticeError     = "error"
	NoticeInvalid   = "invalid"
	NoticeSuccess   = "success"
	NoticeDuplicate = "duplicate"
)

// Notice is a single server-side outcome shown at the top of a screen.
type Notice struct {
	Kind    string
	Message string
}

func (n Notice) Empty() bool { return strings.TrimSpace(n.Message) == "" }

// LocationRef is a site the current user can reach, as the screens name it.
type LocationRef struct {
	ID     string
	Name   string
	Active bool
}

// Price is one item's amount in the shape the garage sells it.
//
// Amounts are integer cents. A price that reaches a customer must never travel
// through a float: 0.1 + 0.2 is a bug in a quote.
type Price struct {
	Kind      string
	Cents     int64 // the amount, or the low end of a range
	MaxCents  int64 // the high end; range only
	TaxBasis  string
	VATRate   int // basis points, so 2000 is 20 %
	PerHour   bool
	Currency  string // reserved; the screens render euros
	Untracked bool   // reserved for an amount the store could not read
}

// Quoted reports whether the price is an amount at all. "Sur devis" is a valid
// answer for the agent to give, but there is no number behind it.
func (p Price) Quoted() bool { return p.Kind != PriceQuote }

// Item is one line of the catalog.
type Item struct {
	ID            string
	Kind          string
	Name          string
	Reference     string
	Description   string
	Price         Price
	Duration      int // minutes; services and packages
	EffectiveFrom time.Time
	EffectiveTo   time.Time
	// LocationNames is empty when the item applies to every site. The handler
	// resolves the names; the view never looks an id up.
	LocationNames []string
}

// Everywhere reports whether the item applies to every site.
func (i Item) Everywhere() bool { return len(i.LocationNames) == 0 }

// Availability places the item against a moment. EffectiveTo is inclusive: a
// price valid "jusqu'au 31 décembre" still holds all day on the 31st.
func (i Item) Availability(now time.Time) string {
	switch {
	case !i.EffectiveFrom.IsZero() && startOfDay(i.EffectiveFrom).After(startOfDay(now)):
		return AvailabilityScheduled
	case !i.EffectiveTo.IsZero() && startOfDay(now).After(startOfDay(i.EffectiveTo)):
		return AvailabilityExpired
	default:
		return AvailabilityActive
	}
}

// Quotable reports whether the agent may say this price out loud right now.
func (i Item) Quotable(now time.Time) bool { return i.Availability(now) == AvailabilityActive }

// Index backs the catalog list.
type Index struct {
	Organization string
	Now          time.Time
	// Kind filters the list; empty shows every kind. Query is the search text.
	// Both are echoed back so the controls keep their state.
	Kind      string
	Query     string
	Items     []Item
	Counts    map[string]int // per kind, over the whole catalog, not the filter
	Locations []LocationRef
	CanManage bool
	Notice    Notice
}

// Count is the number of items of a kind across the whole catalog. It is used
// for the filter tabs, so it must not be affected by the current filter.
func (x Index) Count(kind string) int { return x.Counts[kind] }

// Total counts the whole catalog, again ignoring the filter.
func (x Index) Total() int {
	total := 0
	for _, kind := range Kinds {
		total += x.Counts[kind]
	}
	return total
}

// Filtering reports whether the list is showing a subset, which is what tells
// an empty result apart from an empty catalog.
func (x Index) Filtering() bool { return x.Kind != "" || strings.TrimSpace(x.Query) != "" }

// Stranded reports that the user reaches no site at all. Nothing can be priced
// in that state, so the screen offers sites instead of an unusable form.
func (x Index) Stranded() bool { return len(x.Locations) == 0 }

// Option is one entry of a select control.
type Option struct {
	Value string
	Label string
}

// FormValues holds the editable fields, keyed like the POST body.
//
// Everything is a string, including the amounts and dates: a rejected form has
// to redisplay exactly what the person typed, not a value that failed to parse.
type FormValues struct {
	Kind        string
	Name        string
	Reference   string
	Description string
	PriceKind   string
	Amount      string
	MaxAmount   string
	TaxBasis    string
	VATRate     string
	Duration    string
	From        string // YYYY-MM-DD, as <input type="date"> posts it
	To          string
	Scope       string
	LocationIDs []string
}

// Selected reports whether a site is ticked, so the checkbox list can be
// rendered from the available sites rather than from the selection.
func (v FormValues) Selected(id string) bool {
	for _, selected := range v.LocationIDs {
		if selected == id {
			return true
		}
	}
	return false
}

// FormPage backs the create and edit screens.
type FormPage struct {
	ID           string // empty means create
	Organization string
	Values       FormValues
	VATRates     []Option
	Locations    []LocationRef
	FieldErrors  map[string]string
	Notice       Notice
	CanManage    bool
	// Deletable is false for an item an import owns or that history refers to;
	// the handler decides, the screen only obeys.
	Deletable bool
}

func (p FormPage) Editing() bool { return p.ID != "" }

func (p FormPage) Error(field string) string { return p.FieldErrors[field] }

func (p FormPage) HasError(field string) bool { return p.FieldErrors[field] != "" }

// Stranded mirrors Index.Stranded: no reachable site, nothing to price.
func (p FormPage) Stranded() bool { return len(p.Locations) == 0 }

// Import is one staged file, in the list and at the top of its preview.
type Import struct {
	ID           string
	Filename     string
	LocationName string
	UploadedBy   string
	UploadedAt   time.Time
	Status       string
	Mode         string // the mode it was published with; empty until then
	Valid        int
	Ambiguous    int
	Rejected     int
	// Reason is set when Status is ImportRejected: the file never got as far
	// as having rows.
	Reason string
}

func (i Import) Analyzing() bool { return i.Status == ImportAnalyzing }

func (i Import) Ready() bool { return i.Status == ImportReady }

func (i Import) Published() bool { return i.Status == ImportPublished }

func (i Import) Refused() bool { return i.Status == ImportRejected }

// Rows counts every classified line of the file.
func (i Import) Rows() int { return i.Valid + i.Ambiguous + i.Rejected }

// Partial reports a file that is neither clean nor useless: some rows import,
// some do not. It is the common case, and the one worth saying out loud.
func (i Import) Partial() bool { return i.Valid > 0 && i.Ambiguous+i.Rejected > 0 }

// Usable reports whether publishing this import would put anything in the
// catalog at all.
func (i Import) Usable() bool { return i.Valid+i.Ambiguous > 0 }

// Row is one line of the uploaded file after classification.
type Row struct {
	Number int // the line number in the file, as the person would count it
	Status string
	Name   string
	Kind   string
	// Price is the parsed price; only meaningful when the row is not rejected
	// on its price.
	Price Price
	// Issue explains an ambiguous or rejected row in the garage's words.
	Issue string
	// Collides names the existing catalog item an ambiguous row matches.
	Collides string
}

// Plan is what publishing an import would do, for one chosen mode. The handler
// computes it; the screen only shows it and asks for confirmation.
type Plan struct {
	Create int
	Update int
	Remove int
	Skip   int
}

// Destructive reports whether publishing deletes anything. It gates the extra
// confirmation, and it is the only reason that confirmation exists.
func (p Plan) Destructive() bool { return p.Remove > 0 }

// Changes counts what publishing would write.
func (p Plan) Changes() int { return p.Create + p.Update + p.Remove }

// Imports backs the list of staged files.
type Imports struct {
	Organization string
	Imports      []Import
	CanManage    bool
	Notice       Notice
}

// Upload backs the file drop screen.
type Upload struct {
	Organization string
	Locations    []LocationRef
	// Location is the pre-selected site, echoed back after a refusal.
	Location string
	// Reason is set when a previous attempt was refused, using one of the
	// Reject* constants.
	Reason      string
	FieldErrors map[string]string
	Notice      Notice
}

func (u Upload) Error(field string) string { return u.FieldErrors[field] }

func (u Upload) HasError(field string) bool { return u.FieldErrors[field] != "" }

func (u Upload) Refused() bool { return u.Reason != "" }

func (u Upload) Stranded() bool { return len(u.Locations) == 0 }

// Preview backs the screen that shows the classified rows and asks for a
// decision.
type Preview struct {
	Organization string
	Import       Import
	Rows         []Row
	// Mode is the mode currently being considered. It is empty until the person
	// picks one, and no publish control exists before that.
	Mode string
	// Plan describes what publishing in Mode would do. It is only filled once
	// Mode is set, because merge and replace do not do the same thing.
	Plan      Plan
	CanManage bool
	Notice    Notice
}

// Chosen reports whether a mode has been picked, which is what unlocks the
// publish step.
func (p Preview) Chosen() bool { return p.Mode == ModeMerge || p.Mode == ModeReplace }

// Group returns the rows of one classification, in file order as given.
func (p Preview) Group(status string) []Row {
	rows := make([]Row, 0, len(p.Rows))
	for _, row := range p.Rows {
		if row.Status == status {
			rows = append(rows, row)
		}
	}
	return rows
}

// ---- labels -------------------------------------------------------------

// kindLabel is the singular French name of an item kind.
func kindLabel(kind string) string {
	switch kind {
	case KindService:
		return "Prestation"
	case KindProduct:
		return "Pièce"
	case KindPackage:
		return "Forfait"
	case KindSupplement:
		return "Supplément"
	case KindLabourRate:
		return "Taux horaire"
	}
	return kind
}

// kindPlural names a tab, where a count sits next to it.
func kindPlural(kind string) string {
	switch kind {
	case KindService:
		return "Prestations"
	case KindProduct:
		return "Pièces"
	case KindPackage:
		return "Forfaits"
	case KindSupplement:
		return "Suppléments"
	case KindLabourRate:
		return "Taux horaires"
	}
	return kind
}

func priceKindLabel(kind string) string {
	switch kind {
	case PriceFixed:
		return "Prix ferme"
	case PriceFrom:
		return "À partir de"
	case PriceRange:
		return "Fourchette"
	case PriceQuote:
		return "Sur devis"
	}
	return kind
}

func taxLabel(basis string) string {
	switch basis {
	case TaxExclusive:
		return "HT"
	case TaxInclusive:
		return "TTC"
	}
	return basis
}

func availabilityLabel(state string) string {
	switch state {
	case AvailabilityActive:
		return "En vigueur"
	case AvailabilityScheduled:
		return "À venir"
	case AvailabilityExpired:
		return "Échu"
	}
	return state
}

// priceKindHint says what each way of pricing promises, in the words a garage
// would use on the phone.
func priceKindHint(kind string) string {
	switch kind {
	case PriceFixed:
		return "Le montant annoncé est le montant facturé."
	case PriceFrom:
		return "Le prix plancher ; il peut monter selon le véhicule."
	case PriceRange:
		return "Une fourchette annoncée, du moins cher au plus cher."
	case PriceQuote:
		return "Aucun montant annoncé : l'agent propose un passage à l'atelier."
	}
	return ""
}

// taxBasisLabel spells the basis out in full for a radio label, where "HT"
// alone is a shorthand not everyone reads the same way.
func taxBasisLabel(basis string) string {
	switch basis {
	case TaxExclusive:
		return "Hors taxes (HT)"
	case TaxInclusive:
		return "Toutes taxes comprises (TTC)"
	}
	return basis
}

func rowGroupTitle(status string) string {
	switch status {
	case RowValid:
		return "Lignes valides"
	case RowAmbiguous:
		return "Lignes à vérifier"
	case RowRejected:
		return "Lignes rejetées"
	}
	return status
}

func rowGroupHint(status string) string {
	switch status {
	case RowValid:
		return "Elles seront ajoutées telles quelles."
	case RowAmbiguous:
		return "Elles existent déjà dans votre catalogue. Le mode choisi décide de leur sort."
	case RowRejected:
		return "Elles ne seront pas importées, quel que soit le mode choisi."
	}
	return ""
}

// importStatusColor tints a badge. It only ever repeats what the label already
// says, so nothing depends on the colour alone.
func importStatusColor(status string) string {
	switch status {
	case ImportPublished:
		return "badge-success"
	case ImportReady:
		return "badge-warning"
	case ImportRejected:
		return "badge-error"
	}
	return ""
}

// countsSentence sums an import up in one line for a list card.
func countsSentence(imp Import) string {
	if imp.Rows() == 0 {
		return "Aucune ligne exploitable."
	}
	parts := []string{rowSummary(imp.Valid, "ligne valide", "lignes valides")}
	if imp.Ambiguous > 0 {
		parts = append(parts, rowSummary(imp.Ambiguous, "à vérifier", "à vérifier"))
	}
	if imp.Rejected > 0 {
		parts = append(parts, rowSummary(imp.Rejected, "rejetée", "rejetées"))
	}
	return strings.Join(parts, ", ") + "."
}

// formTitle names the screen. A new item has no name to show yet.
func formTitle(p FormPage) string {
	if !p.Editing() {
		return "Nouvelle ligne"
	}
	if name := strings.TrimSpace(p.Values.Name); name != "" {
		return name
	}
	return "Modifier la ligne"
}

// currentFilter renders aria-current for a control that is not a page link.
// "true" marks the active one; "false" is the ARIA-defined way to say it is
// not.
func currentFilter(active bool) string {
	if active {
		return "true"
	}
	return "false"
}

// currentPage renders aria-current for a link that loads another URL, which is
// what the kind filter does. It is a different value from currentFilter on
// purpose: "page" is only correct when following the link changes the page.
func currentPage(active bool) string {
	if active {
		return "page"
	}
	return "false"
}

// intText writes a count for a badge.
func intText(value int) string { return strconv.Itoa(value) }

func modeLabel(mode string) string {
	switch mode {
	case ModeMerge:
		return "Compléter"
	case ModeReplace:
		return "Remplacer"
	}
	return mode
}

func importStatusLabel(status string) string {
	switch status {
	case ImportAnalyzing:
		return "Analyse en cours"
	case ImportReady:
		return "À valider"
	case ImportPublished:
		return "Publié"
	case ImportRejected:
		return "Refusé"
	case ImportCancelled:
		return "Abandonné"
	}
	return status
}

// rejectionMessage says why a file was refused, and what to do about it.
func rejectionMessage(reason string) string {
	switch reason {
	case RejectUnsupported:
		return "Ce format n'est pas lisible. Exportez votre catalogue en CSV ou en XLSX depuis votre logiciel, puis réessayez."
	case RejectTooLarge:
		return "Ce fichier dépasse " + uploadLimitLabel() + ". Découpez-le, par exemple un fichier par site."
	case RejectEmpty:
		return "Ce fichier ne contient aucune ligne. Vérifiez que l'export a bien fonctionné."
	case RejectUnreadable:
		return "Ce fichier n'a pas pu être ouvert. Il est peut-être incomplet : réexportez-le et réessayez."
	case RejectNoColumns:
		return "Les colonnes attendues sont introuvables. Il faut au minimum une colonne pour le libellé et une pour le prix."
	}
	return "Ce fichier n'a pas pu être utilisé."
}

// uploadLimitLabel writes MaxUploadBytes the way a person reads it.
func uploadLimitLabel() string { return strconv.FormatInt(MaxUploadBytes>>20, 10) + " Mo" }

// ---- formatting ---------------------------------------------------------

// nbsp keeps an amount and its unit on the same line, which is also the French
// typographic rule before a currency sign and a percent sign.
const nbsp = " "

// formatCents writes an integer amount of cents as "1 234,50 €".
func formatCents(cents int64) string {
	sign := ""
	if cents < 0 {
		sign, cents = "-", -cents
	}
	return sign + groupThousands(cents/100) + "," + pad2(int(cents%100)) + nbsp + "€"
}

// groupThousands inserts a non-breaking space every three digits.
func groupThousands(units int64) string {
	digits := strconv.FormatInt(units, 10)
	if len(digits) <= 3 {
		return digits
	}
	var out strings.Builder
	lead := len(digits) % 3
	if lead > 0 {
		out.WriteString(digits[:lead])
	}
	for i := lead; i < len(digits); i += 3 {
		if out.Len() > 0 {
			out.WriteString(nbsp)
		}
		out.WriteString(digits[i : i+3])
	}
	return out.String()
}

// formatRate writes VAT basis points as "20 %" or "5,5 %".
func formatRate(basisPoints int) string {
	whole := basisPoints / 100
	frac := basisPoints % 100
	out := strconv.Itoa(whole)
	if frac != 0 {
		out += "," + strings.TrimRight(pad2(frac), "0")
	}
	return out + nbsp + "%"
}

// priceLabel is the whole price in one sentence fragment, the way it would be
// said on the phone.
func priceLabel(p Price) string {
	if p.Kind == PriceQuote {
		return "Sur devis"
	}
	amount := formatCents(p.Cents)
	switch p.Kind {
	case PriceFrom:
		amount = "À partir de " + amount
	case PriceRange:
		amount = "De " + amount + " à " + formatCents(p.MaxCents)
	}
	if p.PerHour {
		amount += "/h"
	}
	if basis := taxLabel(p.TaxBasis); basis != "" {
		amount += " " + basis
	}
	return amount
}

// vatLabel spells the VAT out for the detail line. A zero rate is a real
// choice (exempt), so it is written rather than hidden.
func vatLabel(p Price) string {
	if p.Kind == PriceQuote {
		return ""
	}
	return "TVA " + formatRate(p.VATRate)
}

// durationLabel writes a service length the way a garage says it: "1 h 30".
func durationLabel(minutes int) string {
	if minutes <= 0 {
		return ""
	}
	hours, rest := minutes/60, minutes%60
	switch {
	case hours == 0:
		return strconv.Itoa(rest) + nbsp + "min"
	case rest == 0:
		return strconv.Itoa(hours) + nbsp + "h"
	default:
		return strconv.Itoa(hours) + nbsp + "h" + nbsp + pad2(rest)
	}
}

// effectiveLabel says how long a price holds. Both dates are optional and each
// absence means something different, so all four cases are spelled out.
func effectiveLabel(from time.Time, to time.Time) string {
	switch {
	case from.IsZero() && to.IsZero():
		return "Sans date de fin"
	case from.IsZero():
		return "Jusqu'au " + formatDate(to)
	case to.IsZero():
		return "À partir du " + formatDate(from)
	default:
		return "Du " + formatDate(from) + " au " + formatDate(to)
	}
}

// locationsLabel names the sites an item applies to.
func locationsLabel(names []string) string {
	if len(names) == 0 {
		return "Tous les sites"
	}
	return strings.Join(names, ", ")
}

// itemSummary counts the visible items, agreeing in number.
func itemSummary(count int) string {
	switch count {
	case 0:
		return "Aucune ligne"
	case 1:
		return "1 ligne"
	}
	return strconv.Itoa(count) + " lignes"
}

// rowSummary counts classified rows for a heading.
func rowSummary(count int, singular string, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return strconv.Itoa(count) + " " + plural
}

var frenchMonths = [...]string{
	"janvier", "février", "mars", "avril", "mai", "juin",
	"juillet", "août", "septembre", "octobre", "novembre", "décembre",
}

// formatDate writes "12 mars 2026". It duplicates ui.FormatDate on purpose:
// pulling it in would be the only reason this package depends on ui beyond the
// layout, and the shared helper is a candidate to move here or there once a
// third caller appears.
func formatDate(at time.Time) string {
	if at.IsZero() {
		return "date inconnue"
	}
	return strconv.Itoa(at.Day()) + " " + frenchMonths[int(at.Month())-1] + " " + strconv.Itoa(at.Year())
}

func startOfDay(at time.Time) time.Time {
	year, month, day := at.Date()
	return time.Date(year, month, day, 0, 0, 0, 0, at.Location())
}

func pad2(value int) string {
	if value < 10 {
		return "0" + strconv.Itoa(value)
	}
	return strconv.Itoa(value)
}

// ---- paths --------------------------------------------------------------

func itemPath(item Item) string { return "/catalog/" + item.ID }

func formActionPath(p FormPage) string {
	if p.Editing() {
		return "/catalog/" + p.ID
	}
	return "/catalog"
}

func deletePath(p FormPage) string { return "/catalog/" + p.ID + "/delete" }

func importPath(imp Import) string { return "/catalog/imports/" + imp.ID }

// modePath re-renders the preview for another mode. It is a link, not a form,
// because choosing a mode changes nothing: it only asks what publishing would
// do.
func modePath(imp Import, mode string) string {
	return "/catalog/imports/" + imp.ID + "?" + FieldMode + "=" + mode
}

func publishPath(imp Import) string { return "/catalog/imports/" + imp.ID + "/publish" }

func cancelPath(imp Import) string { return "/catalog/imports/" + imp.ID + "/cancel" }

// filterPath keeps the search text while switching kind tabs, so a filter is
// never silently dropped.
func filterPath(x Index, kind string) string {
	path := "/catalog"
	params := make([]string, 0, 2)
	if kind != "" {
		params = append(params, "kind="+kind)
	}
	if query := strings.TrimSpace(x.Query); query != "" {
		params = append(params, "q="+queryEscape(query))
	}
	if len(params) > 0 {
		path += "?" + strings.Join(params, "&")
	}
	return path
}

// queryEscape is url.QueryEscape, kept local so the view layer has one import
// less; it only ever sees a search string.
func queryEscape(value string) string {
	var out strings.Builder
	for i := range len(value) {
		c := value[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			out.WriteByte(c)
		case c == ' ':
			out.WriteByte('+')
		default:
			out.WriteByte('%')
			out.WriteByte("0123456789ABCDEF"[c>>4])
			out.WriteByte("0123456789ABCDEF"[c&0x0f])
		}
	}
	return out.String()
}

// ---- notices ------------------------------------------------------------

func noticeTitle(kind string) string {
	switch kind {
	case NoticeSuccess:
		return "C'est enregistré"
	case NoticeInvalid:
		return "Vérifiez les informations ci-dessous"
	case NoticeDuplicate:
		return "Cette référence existe déjà"
	default:
		return "Action impossible pour le moment"
	}
}

func noticeColor(kind string) string {
	switch kind {
	case NoticeSuccess:
		return "alert-success"
	case NoticeInvalid, NoticeDuplicate:
		return "alert-error"
	default:
		return "alert-warning"
	}
}

// ariaInvalid renders the attribute value; "false" is the ARIA-defined way to
// say a control is currently valid.
func ariaInvalid(hasError bool) string {
	if hasError {
		return "true"
	}
	return "false"
}

// describedBy links a control to its hint and, when present, its error.
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
