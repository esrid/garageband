# Pricing model — verified provider costs and launch offer

Verified against official sources 2026-08-01. Re-verify before any contract
or public price list. FX assumption: 1 EUR ≈ 1.07 USD.

## Per-minute call cost (cascaded pipeline: STT → LLM → TTS)

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
multi-site positioning work: at 249 €/site with an average of 3 sites, one
client is ~747 €/month.

| Clients (3 sites avg) | Revenue | Provider+infra | ~21% charges | Net |
|---|---|---|---|---|
| 5 | 3 735 € | ~480 € | ~784 € | **~2 470 €** |
| 4 | 2 988 € | ~384 € | ~627 € | **~1 980 €** |

A single-site client at 249 € nets ~165 €/month after costs and charges —
which is why solo garages are taken opportunistically but are not the pitch.
This is the floor, not the ambition.

## Launch offer (single price — tiers deferred until WhatsApp + catalog exist)

**Target: organizations with 2–6 locations.** Solo-garage deals are accepted
opportunistically at the same price, but they are not what the pitch is built
around — see the competitive landscape below for why.

- **249 €/month per location**, 700 included minutes (~230 calls), overage
  0.30 €/min, hard cap with auto-upgrade prompt.
- Design partners (first 5): **249 €/month locked 12 months**, setup free
  (390 € value), in exchange for weekly feedback and measured call data. The
  concession is the locked price and the free setup, not a lower rate — see
  design-partner-order-form.md, which is the contractual wording.
- Cost at expected usage (~400 min) ≈ 24 € + infra ≈ **~32 € → ~87% gross
  margin**. A fully saturated site (700 min ≈ 42 € + infra ≈ 48 €) still
  holds **~81%**.
- Value anchor for the pitch: a part-time receptionist costs 1 200 €+/month
  loaded; a human telesecretariat covers office hours only; 249 € buys 24/7
  answering + booking. One saved job per week pays for it
  (average garage invoice figure: [À VALIDER — ask in prospecting calls]).
- Split into 3 tiers only when the differentiators (WhatsApp, catalog,
  internal assistant) are implemented — see PRODUCT_FEATURES.md.

## Competitive landscape (verified 2026-08-01, vendor list prices)

| Competitor | Market | Garage-specific? | Price | Included |
|---|---|---|---|---|
| **Tala** (tala-assistant.com) | FR | ✅ vertical page: booking, plate lookup, repair status, recall campaigns, DMS connection | 29 € (50 min) / **99 € (800 min)** / 249 € (2 500 min) / 499 € (9 000 min) HT, sans engagement | minutes bundled |
| **MonAgent-IA** | FR | ✅ WhatsApp chatbot garage | 79–149 €/mois, sur-mesure 500 €+ | — |
| **Clotilde.ai** | FR | ✅ back-office agents (devis, rappels CT, pièces) — **no call answering** | opaque, bundle −20% | — |
| Nerolia / Sylen (agencies) | FR | content marketing + custom builds | 80–350 €/mois cited | — |
| **Numa** | US | ✅ dealerships/service depts | quote-only, ~$200–400/rooftop reported | pay-per-booked-appointment coming |
| Goodcall / Smith.ai | US | generic receptionist | $79+ / $95+ (managed $500) | — |

Market reality: a solo French garage can buy an AI phone agent for
**99–249 €/month today**. This is what sets the ceiling: 249 € sits at Tala's
Pro price point, so the offer never has to argue about being 2–6× the market
on a V1 that does not yet ship its differentiators. Anything above that
requires those differentiators to be live first.

### Differentiators competitors do NOT have (per PRODUCT_FEATURES.md)

- Multi-location organizations: RLS isolation, per-site agents/numbers,
  cross-site customer dossier sharing with audit trail.
- One public number for phone + WhatsApp with per-location routing.
- Catalog-grounded price answers (never invents a price) + staged imports.
- Structured agent memories with human review and provenance.

These matter to **groupes/réseaux multi-sites**, not to the solo garage Tala
serves. That is the whole reconciliation between the market price and the
4–5 client goal: the same 249 € that is merely competitive for one garage
becomes ~747 €/month once a client runs three of them. The offer above is
priced per location for that reason.

**Billing unit: one location = one price.** A three-site group pays 3 × 249 €.
Each site gets its own number, its own agent, and its own included minutes;
minutes do not pool across sites in V1 (see the open item below).

How that is actually collected — one Stripe subscription with a quantity, what
triggers a quantity change, and when any of it is worth automating — is in
`billing-model.md`.

## Open items

- [ ] Pull exact Meta EUR utility rate from the rate-card CSV.
- [ ] Verify Twilio Media Streams price in the Twilio console.
- [ ] Measure real average call duration on tracer bullet; recalc.
- [ ] Willingness-to-pay: 5 garage owner conversations (see prospecting script).
- [ ] Decide whether included minutes pool across a group's sites. Pooling is
      what a multi-site buyer will ask for first (a quiet site subsidising a
      busy one), and it costs nothing extra — the provider bill is per minute,
      not per site. Deferred only until metering exists.
- [ ] Decide whether a group discount applies past ~4 sites. None today: the
      price is flat per location, and a 6-site group pays 1 494 €/month.
