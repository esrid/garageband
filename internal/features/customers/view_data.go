// Package customers renders the customer search and list screen.
//
// It owns no data and talks to no database: a handler builds these view models
// from the store. The contract is written down in docs/customers-ui-contract.md.
// The models are local on purpose — a feature never imports another feature.
package customers

import (
	"strconv"
	"strings"
	"time"
)

// FieldQuery is the search input name, and therefore the query-string key the
// handler reads. Single source of truth for both sides.
const FieldQuery = "q"

// Notice kinds. The view derives the heading from the kind, so French copy
// stays in the view layer instead of leaking into handlers.
const (
	NoticeError = "error"
)

// Notice is a single server-side outcome shown at the top of the screen.
type Notice struct {
	Kind    string
	Message string
}

func (n Notice) Empty() bool { return strings.TrimSpace(n.Message) == "" }

// Vehicle is the little the list needs to show about a car.
type Vehicle struct {
	Plate string // registration plate, already formatted for display
	Model string // "Renault Clio", or whatever of make/model is known
}

// Label is what to print for one vehicle: the plate leads, since that is what
// a garage recognises, with the model as context when it is known.
func (v Vehicle) Label() string {
	switch {
	case v.Plate != "" && v.Model != "":
		return v.Plate + " · " + v.Model
	case v.Plate != "":
		return v.Plate
	default:
		return v.Model
	}
}

// Customer is one search result.
type Customer struct {
	ID          string
	FirstName   string
	LastName    string
	CompanyName string
	Phone       string // primary contact, already formatted
	Email       string // primary contact
	Vehicles    []Vehicle
	// HomeLocationName is the site that owns the record. Shared says this
	// customer reaches you through an explicit grant rather than ownership.
	HomeLocationName string
	Shared           bool
}

// Label composes the display name. The database guarantees at least one of the
// three identity fields, so this never returns empty for a real record.
func (c Customer) Label() string {
	person := strings.TrimSpace(strings.TrimSpace(c.FirstName) + " " + strings.TrimSpace(c.LastName))
	switch {
	case c.CompanyName != "" && person != "":
		return c.CompanyName + " — " + person
	case c.CompanyName != "":
		return c.CompanyName
	default:
		return person
	}
}

// Contacts is the reachable details, in the order a garage uses them: the
// phone first, because that is how customers call.
func (c Customer) Contacts() []string {
	var contacts []string
	if c.Phone != "" {
		contacts = append(contacts, c.Phone)
	}
	if c.Email != "" {
		contacts = append(contacts, c.Email)
	}
	return contacts
}

// Page backs the "Clients" screen.
type Page struct {
	Organization string
	Query        string // what was searched; empty means nothing was asked yet
	Customers    []Customer
	Notice       Notice
}

// Searched reports whether the visitor actually asked something, which is what
// separates "start typing" from "nothing matched".
func (p Page) Searched() bool { return strings.TrimSpace(p.Query) != "" }

// ResultSummary counts the hits in words, agreeing in number.
func ResultSummary(count int) string {
	if count == 1 {
		return "1 client trouvé"
	}
	return strconv.Itoa(count) + " clients trouvés"
}

// vehicleSummary lists the plates a customer drives, capped so one fleet
// customer cannot push every other result off the screen.
func vehicleSummary(vehicles []Vehicle) string {
	const shown = 3
	labels := make([]string, 0, shown)
	for index, vehicle := range vehicles {
		if index == shown {
			labels = append(labels, "+"+strconv.Itoa(len(vehicles)-shown))
			break
		}
		labels = append(labels, vehicle.Label())
	}
	return strings.Join(labels, ", ")
}

func customerPath(customer Customer) string { return "/customers/" + customer.ID }

func noticeTitle(string) string { return "Action impossible pour le moment" }

func noticeColor(string) string { return "alert-warning" }

// ---------------------------------------------------------------------------
// Customer profile
// ---------------------------------------------------------------------------

// Event kinds on the customer timeline.
const (
	EventAppointment = "appointment"
	EventRepair      = "repair"
)

// ProfileVehicle is one car on the profile, where there is room for more than
// the search list shows.
type ProfileVehicle struct {
	ID    string
	Plate string
	Make  string
	Model string
	Year  int
	VIN   string
}

// Label names the vehicle the way a garage says it out loud.
func (v ProfileVehicle) Label() string {
	parts := make([]string, 0, 2)
	if make := strings.TrimSpace(v.Make + " " + v.Model); make != "" {
		parts = append(parts, make)
	}
	if v.Year > 0 {
		parts = append(parts, strconv.Itoa(v.Year))
	}
	if len(parts) == 0 {
		return "Véhicule sans description"
	}
	return strings.Join(parts, " · ")
}

// Event is one entry of the repair and appointment timeline.
type Event struct {
	ID           string
	Kind         string // EventAppointment or EventRepair
	At           time.Time
	Title        string // what was asked or done; may be empty
	VehicleLabel string
	Status       string
	LocationName string
	// AuthoredHere says the active location wrote this record. A shared dossier
	// shows entries from other workshops, which this location may read but not
	// change.
	AuthoredHere bool
	AmountCents  int
	Currency     string
}

// Memory is something the telephone agent retained about the customer.
type Memory struct {
	Key        string
	Value      string
	Status     string  // active, superseded, rejected
	Confidence float64 // 0 when the provider gave none
}

// Profile backs the customer profile screen.
type Profile struct {
	Organization string
	Customer     Customer
	Vehicles     []ProfileVehicle
	Timeline     []Event
	Memories     []Memory
	// CanEdit is false when the dossier reaches this location through a grant:
	// it may read everything and author its own work, but not change the
	// common identity or another workshop's records.
	CanEdit bool
	Notice  Notice
}

var frenchMonths = [...]string{
	"janvier", "février", "mars", "avril", "mai", "juin",
	"juillet", "août", "septembre", "octobre", "novembre", "décembre",
}

// formatDate writes a date the French way. Go has no locale support, and a
// garage should not read "March" on a French screen.
func formatDate(at time.Time) string {
	if at.IsZero() {
		return "Date inconnue"
	}
	return strconv.Itoa(at.Day()) + " " + frenchMonths[int(at.Month())-1] + " " + strconv.Itoa(at.Year())
}

// formatAmount renders money from integer cents, French style: a comma for the
// decimal separator and a non-breaking space before the symbol.
func formatAmount(cents int, currency string) string {
	if cents == 0 {
		return ""
	}
	negative := cents < 0
	if negative {
		cents = -cents
	}
	units := strconv.Itoa(cents / 100)
	// Group thousands with a non-breaking space, as French typography wants.
	var grouped strings.Builder
	for index, digit := range units {
		if index > 0 && (len(units)-index)%3 == 0 {
			grouped.WriteString(" ")
		}
		grouped.WriteRune(digit)
	}
	decimals := strconv.Itoa(cents % 100)
	if len(decimals) == 1 {
		decimals = "0" + decimals
	}
	amount := grouped.String() + "," + decimals + " " + currencySymbol(currency)
	if negative {
		return "-" + amount
	}
	return amount
}

func currencySymbol(currency string) string {
	if currency == "EUR" || currency == "" {
		return "€"
	}
	return currency
}

// eventTitle falls back to the kind when nobody wrote a description, so a row
// never renders as a blank line.
func eventTitle(event Event) string {
	if strings.TrimSpace(event.Title) != "" {
		return event.Title
	}
	if event.Kind == EventRepair {
		return "Réparation"
	}
	return "Rendez-vous"
}

func eventKindLabel(kind string) string {
	if kind == EventRepair {
		return "Réparation"
	}
	return "Rendez-vous"
}

// eventStatusLabel translates the database statuses. The two kinds share some
// values and differ on others, so the kind decides.
func eventStatusLabel(event Event) string {
	if event.Kind == EventRepair {
		switch event.Status {
		case "estimate":
			return "Devis"
		case "awaiting_approval":
			return "En attente d'accord"
		case "approved":
			return "Accepté"
		case "in_progress":
			return "En cours"
		case "completed":
			return "Terminée"
		case "cancelled":
			return "Annulée"
		}
		return event.Status
	}
	switch event.Status {
	case "draft":
		return "Brouillon"
	case "pending":
		return "À confirmer"
	case "confirmed":
		return "Confirmé"
	case "in_progress":
		return "En cours"
	case "completed":
		return "Terminé"
	case "cancelled":
		return "Annulé"
	case "no_show":
		return "Non venu"
	}
	return event.Status
}

func memoryStatusLabel(status string) string {
	switch status {
	case "active":
		return "Retenu"
	case "superseded":
		return "Remplacé"
	case "rejected":
		return "Écarté"
	}
	return status
}

// confidenceLabel turns a 0..1 score into words. A number alone means nothing
// to a garage owner, and an absent score must not read as "no confidence".
func confidenceLabel(confidence float64) string {
	switch {
	case confidence <= 0:
		return ""
	case confidence >= 0.85:
		return "Confiance élevée"
	case confidence >= 0.6:
		return "Confiance moyenne"
	default:
		return "Confiance faible"
	}
}
