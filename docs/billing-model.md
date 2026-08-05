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
items[0][price]     = the per-location Price (299 €, or 249 € locked for a
                      design partner)
items[0][quantity]  = 3        # number of sites in service
```

Requirements and limits from the Stripe doc:

- The Price must be `recurring[usage_type] = licensed`. Quantity is only
  accepted on licensed prices — metered ones are billed from usage records.
- All prices in one subscription share a currency.
- Maximum 20 products per subscription. Irrelevant at 2–6 sites, but it is the
  ceiling if per-site line items are ever introduced instead of a quantity.

Stripe issues **one invoice** for the whole group (3 × 299 = 897 €, or 747 €
at the design-partner rate) and
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

## Settled

- A paused location keeps being billed in full. What the price buys during a
  pause is the number staying reserved and the site's configuration intact for
  an immediate restart, not the minutes. Removing the site is how a customer
  stops paying, and it releases the number for good. Article 4 of the order
  form. Nothing to model in Stripe: a paused site is still one unit of
  quantity.
- Included minutes pool across a group's sites; Pro and Network are sold on it,
  and neither can be invoiced before usage metering exists.

## Open decisions

- [ ] VAT. The order form says franchise en base (art. 293 B) marked
      À VALIDER. It changes what the client actually pays and how Stripe Tax is
      configured. Settle it before the first signature, not after. What the
      official sources say, and why the answer is probably "no", is in the
      section below.
- [ ] Re-verify the Stripe EU card fee (~1.5% + 0.25 € in `pricing-model.md`)
      at integration time.

## VAT: what the official sources say

Checked against impots.gouv.fr, BOFiP, and Legifrance on 2026-08-05. This is
reading, not tax advice — an accountant signs off before the first invoice.

**The franchise en base does not survive the business plan.** For services, the
thresholds are **37 500 € of turnover in the previous calendar year** and
**41 250 € in the current one**; crossing the higher one makes you liable for
VAT on the day it happens, and crossing the lower one from 1 January of the
following year. The target scenario in `pricing-model.md` — five groups of
three sites, 4 485 €/month — is **53 820 €/year**. It clears the threshold
roughly a third of the way through a full year, around 3 440 €/month of
recurring revenue: about a dozen single-site customers, or the fourth
three-site group. Five design partners at 249 € (14 940 €/year) stay under it,
so the franchise is true for the first year and false for the plan.

**The overseas derogation appears to be gone.** A 2017 experimental regime set
100 000 € thresholds for businesses established in Guadeloupe, Martinique, and
La Réunion (CGI art. 293 B VII). The version of art. 293 B in force since
2025-03-01 stops at paragraph II and carries no overseas paragraph. Treat the
metropolitan thresholds as the ones that apply and have the accountant confirm
it — this single point is the difference between "franchise until 100 000 €"
and "VAT from 41 250 €".

**Which rate, once liable.** For B2B services the rate follows *the customer's*
place of establishment, not yours: a Martinique garage is invoiced at the DOM
rate (8.5% standard), a mainland garage at 20%. You are the party liable in
both directions. Stripe Tax therefore has to resolve the rate per customer
location, and a price list that reads "299 € HT" must survive being 299 € +
8.5% for one customer and 299 € + 20% for the next.

**Being under the franchise does not exempt you from a VAT number.** Buying or
selling services from or to a business established in another EU member state
requires an intra-community VAT number whatever the amount, with the VAT then
self-assessed and remitted. Twilio bills from Ireland, so this applies from the
first invoice, franchise or not.

**The article reference on invoices changes.** The mandatory mention is
currently "TVA non applicable, article 293 B du CGI". impots.gouv.fr states the
equivalent reference under the recodified tax code takes effect 2026-09-01, and
Legifrance shows art. 293 B repealed as of 2027-01-01 by ordonnance
n° 2025-1247. Whatever wording the order form ships with needs a date on it.

Sources: [micro-entrepreneur VAT
obligations](https://www.impots.gouv.fr/professionnel/questions/je-suis-micro-entrepreneur-ou-la-tete-dune-micro-entreprise-ai-je-des),
[when a micro-entrepreneur becomes VAT
liable](https://www.impots.gouv.fr/professionnel/questions/en-tant-que-micro-entrepreneur-puis-je-etre-redevable-de-la-tva),
[BOI-TVA-DECLA-40-10-10](https://bofip.impots.gouv.fr/bofip/849-PGP.html/identifiant=BOI-TVA-DECLA-40-10-10-20260701),
[CGI art. 293
B](https://www.legifrance.gouv.fr/codes/article_lc/LEGIARTI000042159618/),
[services between mainland France and the
DOM](https://www.impots.gouv.fr/professionnel/tva-sur-prestations-de-services-metropoledom),
[VAT rates in the
DOM](https://www.impots.gouv.fr/professionnel/questions/quels-sont-les-differents-taux-de-tva-applicables-dans-les-dom).

## Not covered here

Dunning and failed payments, invoice branding, usage metering for overage
minutes above a plan's included allowance, annual prepayment, and per-location
invoicing for groups that want their sites billed separately. All are phase 2 or later,
and none block the first client.
