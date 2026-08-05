# Pricing model — verified provider costs, competition, and launch offer

Verified against official sources 2026-08-03. Re-verify before any contract
or public price list. FX assumption: 1 EUR ≈ 1.07 USD.

## Per-minute call cost with ConversationRelay (the chosen runtime)

Checked against twilio.com and retellai.com on 2026-08-05.

| Item | Rate | Source |
|---|---|---|
| ConversationRelay (STT, TTS incl. ElevenLabs, interruption handling) | $0.07/min | twilio.com/en-us/products/conversational-ai/pricing; ElevenLabs inclusion confirmed by Twilio support, 2026-08-05 |
| Voice minutes, billed separately under the Twilio plan | $0.0100/min inbound FR | twilio.com/en-us/voice/pricing/fr |
| LLM | separate, ~$0.008/min on a Haiku-class model (see the cascade table below) | Anthropic |
| **Realistic total** | **≈ $0.09/min ≈ 0.084 €** | |

With the same 25% buffer for retries, silence, and overruns: **plan on
0.11 €/min**, close to double the 0.06 € the scenarios below still use. Renting
the agent layer instead — Retell at $0.055/min of platform plus TTS, LLM, and
telephony, a published example at $0.13/min — lands in the same range, which is
why the decision turned on what has to be written rather than on price.

That does not threaten the offer: 500 included minutes cost about 55 € against
a 299 € price. It does mean the margin table has not been rebuilt on this
basis yet — see the open item.

The ElevenLabs question is settled: Twilio support confirmed on 2026-08-05
that ElevenLabs voices are covered by the per-minute price, with no separate
line. The public pricing page still says "contact sales", so this rests on
support's word — re-check it on the first real invoice. It matters because
ElevenLabs is ConversationRelay's default TTS provider and carries the fr-FR
voice: the French voice costs nothing extra here, where renting the agent
layer elsewhere prices the same voices as an add-on.

## Per-minute cost of a hand-assembled cascade (not the chosen path)

| Item | Provider | Rate | Cost / call-minute | Source |
|---|---|---|---|---|
| Inbound voice, FR local number | Twilio | $0.0100/min | $0.0100 | twilio.com/en-us/voice/pricing/fr (2026-08-01) |
| Media Streams | Twilio | ~$0.004/min | $0.004 | NOT on the FR pricing page — approximate, verify in console |
| STT streaming (monolingual) | Deepgram Nova-3 | $0.0077/min | $0.0077 | deepgram.com pricing via 2026 sources |
| LLM (~4 turns/min, prompt caching) | Claude Haiku 4.5 | $1 in / $5 out per MTok, cache read ≈ 0.1× | ~$0.008 | Anthropic official (cached 2026-06-24) |
| TTS (agent speaks ~50%, ~450 chars/call-min) | Cartesia Sonic | ~$0.038/1k chars | ~$0.017 | cartesia.ai plans (Scale $299 / 8M credits) |
| **Total cascaded** | | | **≈ $0.047 ≈ 0.044 €** | |

With a 25% buffer for retries, silence, and overruns: **plan on 0.06 €/min**.
Average garage call assumed **3 min → ≈ 0.18 €/call**. The 3-min figure is
unvalidated — measure on the first tracer bullet and recalibrate.

TTS caveat: ElevenLabs at standard API rates (~$0.24–0.30/1k chars) is
~$0.10+/call-min — 6× Cartesia. Voice choice is a pricing decision.
Speech-to-speech realtime models remain ~4× the cascaded cost; the V1
pipeline must stay cascaded for the margin to hold.

## Fixed and per-event costs

| Item | Rate | Source |
|---|---|---|
| FR local number | $1.35/month | Twilio FR pricing page (2026-08-01) |
| WhatsApp utility template **inside 24h service window** | **Free** | developers.facebook.com/docs/whatsapp/pricing (2026-08-01) |
| WhatsApp utility template outside window (Western Europe) | < $0.01/msg | Meta EUR rate card — exact figure still to pull from the CSV |
| Infra per tenant (DB, hosting, transcript storage) | 4–8 €/month | internal estimate, not provider-verified |
| Stripe EU card | ~1.5% + 0.25 € | stripe.com — re-verify at Stripe integration time |

The WhatsApp finding matters: appointment confirmations sent as replies within
the 24h window cost **zero**. Only out-of-window reminders are billed.

## Founder constraint: livable income on 4–5 clients

Target: ~3 000 €/month revenue from 5 clients → **~600 €/client/month**.
(If operating as micro-entreprise: social charges apply to *turnover*, not
profit — provider costs are not deductible, so gross margin matters doubly.)

That figure is per *client*, not per site, and this is what makes the
multi-site positioning work: at 299 €/site with an average of 3 sites, one
client is ~897 €/month. The Pro tier lands on the same figure by design, so
the two bottom rows below differ by ten euros rather than by a tier choice.

| Scenario | Revenue | Provider+infra | 25.6% social charges | Net before income tax |
|---|---:|---:|---:|---:|
| 5 Essential, 1 site each | 1 495 € | ~415 € | ~383 € | **~697 €** |
| 5 Pro organizations, 3 sites each, pooled | 4 495 € | ~1 095 € | ~1 151 € | **~2 249 €** |
| 5 groups, 3 sites each billed per site | 4 485 € | ~1 150 € | ~1 148 € | **~2 187 €** |

These are conservative full-allowance scenarios using the revised SMS quotas.
They exclude income tax, insurance, payment fees, accounting, support time and
VAT effects. Five customers only supports a livable founder income when the
customers are multi-site groups billed per location; five single-site garages
do not.

### After income tax: Martinique reference case

Reference assumption: one tax household, one part, no other income, resident in
Martinique, micro-BNC without the optional versement libératoire. Micro-BNC
taxable income uses the 34% forfaitary allowance, then the progressive income
tax scale applies. On the five-group scenario above, the estimated income tax
is approximately **2 675 €/year**, or **223 €/month**, after the 30% Martinique
reduction capped at 2 450 €. The remaining amount is therefore approximately
**1 960 €/month before personal living expenses**. This is an estimate, not a
tax filing calculation; household income and dependants can change it
substantially.

A single-site client at 299 € nets roughly ~200 €/month after costs and charges —
which is why solo garages are taken opportunistically but are not the pitch.
This is the floor, not the ambition.

## Launch offer — one price per location, three managed tiers

Every tier bills the same 299 € per location, at any number of locations. A
tier is a packaging of that same unit — pooling, overage rate, support — never
a discount and never a penalty, so no group can ever be better off buying its
sites one by one or worse off growing into the next tier. There is no volume
discount either: seven garages pay seven times 299 €, and the answer to
"what do you do for a network?" is the tier, not a rebate.

**Target: organizations with 2–6 locations.** Solo-garage deals are accepted
opportunistically at the same price, but they are not what the pitch is built
around — see the competitive landscape below for why.

- Essential — **299 €/month per location**, 500 included minutes, 1 000
  WhatsApp transactional messages, 100 SMS fallback messages, overage
  0.35 €/min, hard cap with auto-upgrade prompt.
- Pro — **899 €/month per organization**, 1 500 included minutes shared across up
  to three locations, 3 000 WhatsApp transactional messages, 400 SMS fallback
  messages, overage 0.25 €/min, separate telephone routes and site contexts,
  and priority support. That is exactly 3 × 299 €: the upper tier is never
  cheaper than buying the same three locations one by one. What it adds at
  the same price is pooling (a quiet site's unused minutes cover a busy one),
  a lower overage rate, 100 extra SMS, and priority support — none of which
  costs more to serve, because the provider bill is per minute, not per site.
- Network — **299 €/month per location for 4–7 locations**, 1 196 € at four sites,
  2 093 € at seven, with every allowance scaling per site and pooled across
  the group: 500 minutes, 1 000 WhatsApp transactional messages, and 100 SMS
  fallback messages each, so seven sites share 3 500 minutes, 7 000 WhatsApp
  messages, and 700 SMS. Overage 0.25 €/min as on Pro, plus routing, priority
  support, and supervision. A seven-site group running 5 000 minutes is billed
  2 093 € + 1 500 × 0.25 € = **2 468 € HT**.
- WhatsApp is sold with a reasonable-use allowance, not literal unlimited
  messaging. Marketing campaigns and usage above the allowance are billed at
  cost plus a handling margin.
- SMS is a separate metered allowance. Additional SMS are billed at provider
  cost plus a handling margin because French outbound SMS rates are materially
  higher than WhatsApp platform fees.
- Design partners (first 5): **249 €/month locked 12 months**, setup free
  (390 € value), in exchange for weekly feedback and measured call data. The
  concession is the locked price and the free setup, not a lower rate — see
  design-partner-order-form.md, which is the contractual wording.
- The allowances are deliberately generous for normal usage but are not
  expected to be fully consumed by every customer. At full revised allowances,
  Essential costs are roughly 70–85 €/month and Pro roughly 200–230 €/month,
  before support and tax. SMS is the main variable risk.
- Value anchor for the pitch: a part-time receptionist costs 1 200 €+/month
  loaded; a human telesecretariat covers office hours only; 299 € buys 24/7
  answering + booking. One saved job per week pays for it
  (average garage invoice figure: [À VALIDER — ask in prospecting calls]).
- The lower tier is the launch default; the upper tier pays for higher call
  volume, more locations, and support rather than an artificial feature wall.
- A network plan must keep a usage overage. A flat price that absorbs 5 000
  minutes at the most expensive voice configuration is not viable in the
  micro-entreprise regime.

## Competitive landscape (checked against official vendor pages 2026-08-03)

| Competitor | Market | Garage-specific? | Price | Included |
|---|---|---|---|---|
| **Tala** (tala-assistant.com) | FR | ✅ vertical page: booking, plate lookup, repair status, recall campaigns, DMS connection | 29 € (50 min) / **99 € (800 min)** / 249 € (2 500 min) / 499 € (9 000 min) HT, sans engagement | minutes bundled |
| **MonAgent-IA** | FR | ✅ WhatsApp chatbot, garage page | 49 / 99 / 199 € HT/mois | 1 000 / 5 000 / négocié ; les pages officielles ne sont pas parfaitement cohérentes |
| **Clotilde.ai** | FR | back-office annoncé | prix non vérifié dans cette recherche | ne pas utiliser comme benchmark tarifaire |
| **Numa** | US | ✅ service departments | prix non public | benchmark produit, pas tarif fiable |
| **Goodcall / Smith.ai** | US | réception générique | non retenus pour la comparaison France/Martinique | numéros et WhatsApp non comparables |

Les fournisseurs techniques évalués séparément sont Retell, Bland, ElevenLabs,
Vapi, Twilio et Telnyx. Ils ne sont pas tous des concurrents directs : certains
fournissent le moteur vocal, d'autres la téléphonie, les numéros ou WhatsApp.

Market reality: a solo French garage can buy a voice-only AI receptionist for
**99–249 €/month today**. The proposed 299 € and 899 € offers therefore need to
sell managed multi-channel operations — configuration, WhatsApp, integration,
number lifecycle, and support — rather than raw minutes alone.

### Differentiators competitors do NOT have (per PRODUCT_FEATURES.md)

- Multi-location organizations: RLS isolation, per-site agents/numbers,
  cross-site customer dossier sharing with audit trail.
- Existing fixed number retained for calls, plus a lifecycle-managed
  Garageband-owned AI/WhatsApp sender when needed.
- Catalog-grounded price answers (never invents a price) + staged imports.
- Structured agent memories with human review and provenance.

These matter to **groupes/réseaux multi-sites**, not to the solo garage Tala
serves. That is the whole reconciliation between the market price and the
4–5 client goal: the managed 299 € tier is above a voice-only receptionist
because it includes multi-channel operations and lifecycle management. Three
locations make one client worth ~897 €/month. The offer above is
priced per location for that reason.

**Billing unit: one location = one price.** A three-site group pays 3 × 299 €
on the lower tier or 899 € on Pro — the same money either way, deliberately.
Every tier is anchored to 299 € per location so that no packaging is ever
cheaper than buying the locations one at a time; a group that grows into Pro
gains pooled minutes and support, never a discount. Each site keeps its own
number, its own agent, and its own context in both cases. Pooling is the one
thing Pro cannot deliver before usage metering exists, which is what the open
item below is really gating.

How that is actually collected — one Stripe subscription with a quantity, what
triggers a quantity change, and when any of it is worth automating — is in
`billing-model.md`.

## Open items

- [ ] Settle VAT with an accountant. The franchise en base written on the order
      form stops being true at 41 250 € of annual turnover, which this offer
      reaches at roughly a dozen single-site customers — the sourced reading is
      in `billing-model.md`. Every figure on this page is stated excluding tax.
- [x] The design-partner order form promised 700 minutes/site at 0.30 €/min
      against Essential's 500 at 0.35 €, contradicting "the concession is the
      locked price and the free setup, not a lower rate" two lines above. The
      form now carries Essential's own allowance: a design partner buys the
      public offer at a locked 249 € with the setup waived, and nothing else
      changes when the twelve months end.
- [ ] Rebuild the margin scenarios on ConversationRelay's per-minute cost.
      Every "provider+infra" figure in the table above was computed at
      0.06 €/min for a hand-assembled cascade; the platform fee roughly
      doubles it. Do it together with the tracer-bullet measurement rather
      than twice.
- [x] ElevenLabs voices are included in ConversationRelay's $0.07/min, per
      Twilio support on 2026-08-05. The pricing page still defers to sales, so
      confirm it against the first real invoice.
- [ ] Price the forward itself for an overseas customer. Twilio has no
      Martinique number, so a Martinique garage forwards its own 0596 line to a
      metropolitan number and its carrier bills that leg. If Orange Caraïbe,
      SFR Caraïbe, or Digicel meter it per minute, every AI-answered call costs
      the customer something on top of the subscription, and that belongs in
      the pitch rather than in their first phone bill.
- [ ] Pull exact Meta EUR utility rate from the rate-card CSV.
- [ ] Verify Twilio Media Streams price in the Twilio console.
- [ ] Measure real average call duration on tracer bullet; recalc.
- [ ] Willingness-to-pay: 5 garage owner conversations (see prospecting script).
- [x] Included minutes pool across a group's sites. It is what a multi-site
      buyer asks for first (a quiet site subsidising a busy one) and it costs
      nothing extra, because the provider bill is per minute, not per site.
      Pro and Network are sold on it, so neither can be sold before usage
      metering exists — that is now an implementation dependency, not an open
      question.
- [x] No group discount, at any number of locations. A six-site group pays
      1 794 €/month and a seven-site one 2 093 €, the same 299 € per garage as
      a solo shop. The reason is the same one that fixes the tiers: one
      sentence to hold in a meeting, no arithmetic in front of the customer,
      and no precedent created for the next prospect who hears about it. At
      five customers, a euro conceded costs double — social charges are
      levied on turnover, not on profit. A discount stays available to unblock
      a hard signature, case by case, precisely because it was never promised.
- [x] The network tier was a flat 2 490 € for 4–7 locations, which its own
      per-location price beat at every site count in that range: seven sites
      bought one by one cost 2 093 € and carried 3 500 included minutes
      against the tier's 2 500. It is now anchored to 299 €/site like the
      others, with allowances scaling per site and pooled.
