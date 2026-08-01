// Package customers renders the customer search and list screen.
//
// It owns no data and talks to no database: a handler builds these view models
// from the store. The contract is written down in docs/customers-ui-contract.md.
// The models are local on purpose — a feature never imports another feature.
package customers

import (
	"strconv"
	"strings"
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
