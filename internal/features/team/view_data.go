// Package team renders the screen where an organization decides which staff
// reach which physical locations.
//
// The views never query anything and never decide permissions: a handler
// builds these view models from the store, and views_test.go is the contract.
// The models are local on purpose — a feature never imports another feature —
// so LocationRef carries only what this screen displays.
package team

import (
	"strconv"
	"strings"
)

// Membership roles, as constrained by tenant_memberships.role.
const (
	RoleOwner   = "owner"
	RoleAdmin   = "admin"
	RoleManager = "manager"
	RoleMember  = "member"
)

// Notice kinds. The view derives the heading from the kind, so French copy
// stays in the view layer instead of leaking into handlers.
const (
	NoticeError   = "error"   // the store or an upstream service failed
	NoticeSuccess = "success" // the last change went through
)

// Notice is a single server-side outcome shown at the top of the screen.
type Notice struct {
	Kind    string
	Message string
}

func (n Notice) Empty() bool { return strings.TrimSpace(n.Message) == "" }

// LocationRef is the little this screen needs to know about a site.
type LocationRef struct {
	ID     string
	Name   string
	Active bool
}

// Invite states shown next to a member who has never signed in.
const (
	InvitePending        = "pending"
	InvitePendingExpired = "expired"
)

// Member is one person in the organization and the sites they reach.
type Member struct {
	UserID string
	Name   string // display name; falls back to the email when empty
	Email  string
	Role   string
	// LocationIDs are the sites explicitly assigned to this member. It stays
	// empty for owners and admins, who reach every site by their role.
	LocationIDs []string
	// InviteState is empty once the person has followed their link.
	InviteState string
}

// Invited reports someone the owner enrolled who has not joined yet.
func (m Member) Invited() bool { return m.InviteState != "" }

// InviteExpired reports a link that ran out before it was used, which needs a
// new one rather than patience.
func (m Member) InviteExpired() bool { return m.InviteState == InvitePendingExpired }

// Removable keeps the owner and the admins out of the remove button. They run
// the organization, and removing one of them is not a routine staff change.
func (m Member) Removable() bool { return !m.ReachesEveryLocation() }

// Label is what to call this person on screen.
func (m Member) Label() string {
	if strings.TrimSpace(m.Name) != "" {
		return m.Name
	}
	return m.Email
}

// ReachesEveryLocation reports the roles that reach every site implicitly, so
// the screen shows their access instead of offering to edit it.
func (m Member) ReachesEveryLocation() bool {
	return m.Role == RoleOwner || m.Role == RoleAdmin
}

func (m Member) HasLocation(id string) bool {
	for _, assigned := range m.LocationIDs {
		if assigned == id {
			return true
		}
	}
	return false
}

// Unassigned marks a member who can sign in but reach nothing, which is worth
// pointing out rather than leaving them silently stuck.
func (m Member) Unassigned() bool {
	return !m.ReachesEveryLocation() && len(m.LocationIDs) == 0
}

// Page backs the "Accès aux sites" screen.
type Page struct {
	Organization string
	Members      []Member
	Locations    []LocationRef // every site of the organization, active or not
	CanManage    bool          // false renders the read-only presentation
	Notice       Notice
	// Invite is set for exactly one render: the response to creating or
	// reissuing an invitation. The database keeps only the code's hash, so a
	// page that does not show it here can never show it again.
	Invite Invitation
	// InvitedName names who InviteLink belongs to, so an owner enrolling three
	// people in a row cannot hand the wrong link to the wrong person.
	InvitedName string
}

// UnassignedCount drives the summary line at the top of the screen.
func (p Page) UnassignedCount() int {
	count := 0
	for _, member := range p.Members {
		if member.Unassigned() {
			count++
		}
	}
	return count
}

// FieldLocations is the checkbox name the assignment form posts, repeated once
// per selected site. It is the single source of truth for both sides.
const FieldLocations = "location_ids"

// FieldName is the invitation form's text input.
const FieldName = "name"

// namePath is where one member's rename form posts.
func namePath(member Member) string {
	return "/team/" + member.UserID + "/name"
}

// nameFieldID keeps each rename input addressable by its own label.
func nameFieldID(member Member) string {
	return "name-" + member.UserID
}

// codePath is where one member's "new code" form posts.
func codePath(member Member) string {
	return "/team/" + member.UserID + "/code"
}

// groupInviteCode breaks the code into groups of four. Twelve unbroken
// characters get miscounted when read out loud across a workshop.
func groupInviteCode(code string) string {
	var grouped strings.Builder
	for i, r := range code {
		if i > 0 && i%4 == 0 {
			grouped.WriteByte('-')
		}
		grouped.WriteRune(r)
	}
	return grouped.String()
}

// revokePath is where one member's removal form posts.
func revokePath(member Member) string {
	return "/team/" + member.UserID + "/revoke"
}

// inviteStateLabel says where someone stands before they have ever signed in.
func inviteStateLabel(member Member) string {
	if member.InviteExpired() {
		return "Lien expiré"
	}
	return "Invitation en attente"
}

// roleLabel is the French name of a membership role.
func roleLabel(role string) string {
	switch role {
	case RoleOwner:
		return "Propriétaire"
	case RoleAdmin:
		return "Administrateur"
	case RoleManager:
		return "Responsable"
	case RoleMember:
		return "Membre"
	default:
		return role
	}
}

// accessSummary states, in words, what a member reaches. The screen never
// leaves that to a count of checked boxes alone.
func accessSummary(member Member, locations []LocationRef) string {
	if member.ReachesEveryLocation() {
		return "Tous les sites, par son rôle"
	}
	names := make([]string, 0, len(member.LocationIDs))
	for _, location := range locations {
		if member.HasLocation(location.ID) {
			names = append(names, location.Name)
		}
	}
	if len(names) == 0 {
		return "Aucun site"
	}
	return strings.Join(names, ", ")
}

// assignmentPath is where one member's assignment form posts.
func assignmentPath(member Member) string {
	return "/team/" + member.UserID + "/locations"
}

func noticeTitle(kind string) string {
	if kind == NoticeSuccess {
		return "C'est enregistré"
	}
	return "Action impossible pour le moment"
}

func noticeColor(kind string) string {
	if kind == NoticeSuccess {
		return "alert-success"
	}
	return "alert-warning"
}

// checkboxID keeps every control on the page uniquely addressable by its label.
func checkboxID(member Member, location LocationRef) string {
	return "assign-" + member.UserID + "-" + location.ID
}

// unassignedHeading counts the people who cannot reach anything yet, agreeing
// in number the way French requires.
func unassignedHeading(count int) string {
	if count == 1 {
		return "1 personne n'a accès à aucun site"
	}
	return strconv.Itoa(count) + " personnes n'ont accès à aucun site"
}
