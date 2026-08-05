package telephony

import "context"

// Subaccount is one customer's container at the carrier. One organization is
// one subaccount: closing it releases every number it holds, which is what
// makes offboarding a single step instead of a checklist.
type Subaccount struct {
	SID    string
	Status string
}

// NumberRequest buys one number for one site.
type NumberRequest struct {
	// SubaccountSID is the customer's container; the number must land there
	// and not on the parent account.
	SubaccountSID string
	E164          string
	// BundleSID is the customer's own compliance file. Regulated countries
	// reject the purchase without it, which is the guard that keeps a number
	// from reaching a garage before that garage is compliant.
	BundleSID  string
	AddressSID string
	// VoiceWebhookURL receives the inbound call and answers with TwiML.
	VoiceWebhookURL string
}

// Number is a number as the carrier now holds it.
type Number struct {
	SID  string
	E164 string
	Capabilities
}

type Capabilities struct {
	Voice bool
	SMS   bool
	MMS   bool
}

// BundleState is what the carrier says about a compliance file. Statuses are
// the carrier's own words, kept verbatim so nothing needs translating on the
// way in.
type BundleState struct {
	SID           string
	Status        string
	FailureReason string
}

// Provisioner is the carrier side of onboarding a garage: give it its own
// container, buy the number its calls will land on, and follow the compliance
// file it cannot be bought without.
type Provisioner interface {
	CreateSubaccount(ctx context.Context, friendlyName string) (Subaccount, error)
	BuyNumber(ctx context.Context, request NumberRequest) (Number, error)
	BundleState(ctx context.Context, bundleSID string) (BundleState, error)
}
