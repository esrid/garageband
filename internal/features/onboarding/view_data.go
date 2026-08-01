package onboarding

import (
	"errors"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// Notice kinds label an outcome for the view: they pick the alert colour, its
// icon and its heading, so a state is never signalled by colour alone.
const (
	noticeNotFound    = "notfound"    // the SIRET is unusable: malformed, unknown, or closed
	noticeUnavailable = "unavailable" // the registry did not answer
	noticeInvalid     = "invalid"     // the confirmation form needs corrections
	noticeExpired     = "expired"     // the draft is too old to finalize
	noticeMismatch    = "mismatch"    // the submitted SIRET is not the one looked up
	noticeDuplicate   = "duplicate"   // the garage or its slug already exists
)

// noticeKindForLookupStatus maps the status the lookup handler already chose
// onto a notice kind, so the handler keeps a single source of truth.
func noticeKindForLookupStatus(status int) string {
	if status >= 500 {
		return noticeUnavailable
	}
	return noticeNotFound
}

func noticeColor(kind string) string {
	switch kind {
	case noticeUnavailable, noticeExpired, noticeMismatch:
		return "alert-warning"
	default:
		return "alert-error"
	}
}

// Interface copy is French; identifiers and comments stay English.
func noticeTitle(kind string) string {
	switch kind {
	case noticeUnavailable:
		return "Le registre ne répond pas"
	case noticeExpired:
		return "Cette recherche a expiré"
	case noticeMismatch:
		return "Ce SIRET ne correspond pas à votre recherche"
	case noticeDuplicate:
		return "Ce garage existe déjà"
	case noticeInvalid:
		return "Vérifiez les informations ci-dessous"
	default:
		return "Nous n'avons pas pu utiliser ce SIRET"
	}
}

// stepMarker is the glyph daisyUI prints inside the step bullet. A tick marks a
// finished step, which is the non-colour signal of progress.
func stepMarker(index int, current int) string {
	if index < current {
		return "✓"
	}
	return strconv.Itoa(index)
}

// stepCurrent yields an aria-current value. "false" is the ARIA-defined way to
// say "not the current step".
func stepCurrent(index int, current int) string {
	if index == current {
		return "step"
	}
	return "false"
}

// duplicateMessage turns a PostgreSQL unique violation into copy that tells the
// owner what to do about it. Anything else is a genuine failure to log.
// Verified against https://pkg.go.dev/github.com/jackc/pgx/v5/pgconn 2026-08-01.
func duplicateMessage(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return "", false
	}
	if strings.Contains(pgErr.ConstraintName, "slug") {
		return "Cet identifiant d'espace est déjà pris. Choisissez-en un autre ci-dessous.", true
	}
	return "Cette entreprise est déjà enregistrée dans Garageband. Demandez à son propriétaire de vous inviter.", true
}

// SIREN is the company number carried inside the SIRET: its first nine digits.
// It is shown read-only, never submitted, and the store derives it again.
func (d formData) SIREN() string {
	if len(d.SIRET) < 9 {
		return ""
	}
	return d.SIRET[:9]
}
