// Package twilio implements the telephony provisioning port against Twilio.
// Verified against https://pkg.go.dev/github.com/twilio/twilio-go 2026-08-05.
package twilio

import (
	"context"
	"errors"
	"strings"

	twiliogo "github.com/twilio/twilio-go"
	api "github.com/twilio/twilio-go/rest/api/v2010"

	"github.com/esrid/garageband/internal/platform/telephony"
)

// Client provisions numbers on Twilio. It holds the parent account's
// credentials; every purchase names the customer's subaccount explicitly so a
// number never lands on the parent by omission.
type Client struct {
	rest *twiliogo.RestClient
}

// New builds a client from the parent account's credentials.
func New(accountSID, authToken string) (*Client, error) {
	if strings.TrimSpace(accountSID) == "" || strings.TrimSpace(authToken) == "" {
		return nil, errors.New("twilio account sid and auth token are required")
	}
	return &Client{rest: twiliogo.NewRestClientWithParams(twiliogo.ClientParams{
		Username: accountSID,
		Password: authToken,
	})}, nil
}

var _ telephony.Provisioner = (*Client)(nil)

// CreateSubaccount gives one garage its own container.
func (c *Client) CreateSubaccount(
	ctx context.Context, friendlyName string,
) (telephony.Subaccount, error) {
	params := &api.CreateAccountParams{}
	params.SetFriendlyName(friendlyName)
	account, err := c.rest.Api.CreateAccount(params)
	if err != nil {
		return telephony.Subaccount{}, err
	}
	subaccount := telephony.Subaccount{}
	if account.Sid != nil {
		subaccount.SID = *account.Sid
	}
	if account.Status != nil {
		subaccount.Status = *account.Status
	}
	if subaccount.SID == "" {
		return telephony.Subaccount{}, errors.New("twilio returned no subaccount sid")
	}
	return subaccount, nil
}

// BuyNumber buys one number into a customer's subaccount and points its voice
// webhook at us in the same call, so a number is never reachable before it
// knows where to send a call.
func (c *Client) BuyNumber(
	ctx context.Context, request telephony.NumberRequest,
) (telephony.Number, error) {
	if strings.TrimSpace(request.SubaccountSID) == "" {
		return telephony.Number{}, errors.New("subaccount sid is required")
	}
	params := &api.CreateIncomingPhoneNumberParams{}
	params.SetPathAccountSid(request.SubaccountSID)
	params.SetPhoneNumber(request.E164)
	params.SetVoiceUrl(request.VoiceWebhookURL)
	params.SetVoiceMethod("POST")
	if request.BundleSID != "" {
		params.SetBundleSid(request.BundleSID)
	}
	if request.AddressSID != "" {
		params.SetAddressSid(request.AddressSID)
	}

	number, err := c.rest.Api.CreateIncomingPhoneNumber(params)
	if err != nil {
		return telephony.Number{}, err
	}
	bought := telephony.Number{}
	if number.Sid != nil {
		bought.SID = *number.Sid
	}
	if number.PhoneNumber != nil {
		bought.E164 = *number.PhoneNumber
	}
	if number.Capabilities != nil {
		bought.Capabilities = readCapabilities(*number.Capabilities)
	}
	if bought.SID == "" {
		return telephony.Number{}, errors.New("twilio returned no number sid")
	}
	return bought, nil
}

// BundleState reports where a customer's compliance file stands. The wait
// between submission and a verdict is the time to activation the order form
// promises, so it is read rather than assumed.
func (c *Client) BundleState(
	ctx context.Context, bundleSID string,
) (telephony.BundleState, error) {
	bundle, err := c.rest.NumbersV2.FetchBundle(bundleSID)
	if err != nil {
		return telephony.BundleState{}, err
	}
	state := telephony.BundleState{SID: bundleSID}
	if bundle.Status != nil {
		state.Status = *bundle.Status
	}
	return state, nil
}

// readCapabilities decodes the untyped capabilities map Twilio returns.
func readCapabilities(raw any) telephony.Capabilities {
	values, ok := raw.(map[string]any)
	if !ok {
		return telephony.Capabilities{}
	}
	flag := func(key string) bool {
		enabled, _ := values[key].(bool)
		return enabled
	}
	return telephony.Capabilities{
		Voice: flag("voice"),
		SMS:   flag("sms"),
		MMS:   flag("mms"),
	}
}
