# Billing model — how the price in pricing-model.md gets charged

Companion to `pricing-model.md`, which decides *what* a client pays. This one
decides *how* it is collected, and when any of it is worth automating.

Stripe mechanics verified against
`docs.stripe.com/billing/subscriptions/quantities` on 2026-08-01. Re-verify
before writing the first API call.

## Billing unit: one location = one quantity unit

A group with three garages does **not** get three subscriptions. It gets one
subscription whose quantity is three:

```
customer            = the organization, not the site
items[0][price]     = the single 249 €/month Price
items[0][quantity]  = 3        # number of sites in service
```

Requirements and limits from the Stripe doc:

- The Price must be `recurring[usage_type] = licensed`. Quantity is only
  accepted on licensed prices — metered ones are billed from usage records.
- All prices in one subscription share a currency.
- Maximum 20 products per subscription. Irrelevant at 2–6 sites, but it is the
  ceiling if per-site line items are ever introduced instead of a quantity.

Stripe issues **one invoice** for the whole group (3 × 249 = 747 €) and
**prorates automatically** whenever the quantity changes mid-period. A site
added on the 15th produces a partial line, not a full month — say so when a
client asks why an invoice moved.

## Today: no code, and that is deliberate

Recurring collection is already automatic once a subscription exists. Stripe
charges the card every month with no involvement from the application. The only
manual act is **creating the subscription once per client** — five times total
for the five design partners, a few minutes each in the Stripe Dashboard.

Building Checkout, webhooks, and subscription state into the app costs days and
saves roughly fifteen minutes at this scale. It is not on the critical path to
the first paying client.

Self-serve signup where a prospect picks a site count is phase 3 below, and
only becomes real when inbound demand exists.

## The one risk that does justify code: quantity drift

The failure mode is not signup. It is a client adding a fourth location in the
app while the subscription still says three. The under-billing is silent —
nothing in Stripe knows a site appeared, and the client has no incentive to
report it. Months of lost revenue with no error anywhere.

That is what phase 2 fixes, and it is not a checkout page. It is one API call
on the events that change how many sites are in service.

## The trigger is service activation, not location creation

Naively wiring "location created ⇒ quantity++" **contradicts the signed order
form**, which states that billing starts at the effective service activation of
the first site's number, verified jointly — not when a row is inserted.

So the billable count is not "rows in `locations`", and it is not "locations
with status `active`" either, since a site can be configured well before its
number answers. The quantity must track **sites whose number is in service**.

| Event | Quantity | Rationale |
|---|---|---|
| Location created in the app | unchanged | Nothing is owed yet; the order form is explicit |
| Number provisioned and answering | +1 | The contractual start of billing |
| Location archived / number released | −1 | Stripe credits the prorated remainder |
| Location paused with the number kept | **undecided** | See open decisions |

Whoever builds this needs the backend to expose the service-activation event.
It does not exist yet.

## Phasing

| Phase | Trigger to start it | Work | Code |
|---|---|---|---|
| 1 — now | 5 design partners | Subscription created in the Dashboard, quantity set by hand | none |
| 2 — first client who adds a site without telling you | quantity drift becomes possible | quantity synced from the app on service activation and release | `internal/platform/billing/` + the trigger |
| 3 — self-serve | real inbound demand | Checkout with an adjustable site count | days |

Phase 3 would use Stripe Checkout's adjustable quantity. **Its field names and
subscription-mode support are not verified here** — read the variant-specific
Checkout docs before designing against it.

## Where phase 2 code goes

Stripe is an external system, so it is `internal/platform/billing/`, one
package, injected into whichever feature owns service activation. It never
imports a feature. If the provider is ever swappable, the port is defined in
the platform package, the way `oauth.Provider` is.

The subscription id belongs to the organization, so it is stored on the tenant
row, and every read of it stays inside the tenant transaction boundary like
everything else.

## Open decisions

- [ ] A location paused with its number retained: still billed, or credited?
      Keeping the number costs 1.35 $/month; releasing it means the client
      loses it and cannot get it back. Retaining it and billing in full is the
      defensible answer, but it must be written into the order form before it
      is charged to anyone.
- [ ] Do included minutes pool across a group's sites? Pooling is the first
      thing a multi-site buyer asks for and costs nothing — the provider bill
      is per minute, not per site. Blocked only on metering existing. Also
      tracked in `pricing-model.md`.
- [ ] VAT. The order form says franchise en base (art. 293 B) marked
      À VALIDER. It changes what the client actually pays and how Stripe Tax is
      configured. Settle it before the first signature, not after.
- [ ] Re-verify the Stripe EU card fee (~1.5% + 0.25 € in `pricing-model.md`)
      at integration time.

## Not covered here

Dunning and failed payments, invoice branding, usage metering for overage
minutes above the 700 included, annual prepayment, and per-location invoicing
for groups that want their sites billed separately. All are phase 2 or later,
and none block the first client.
