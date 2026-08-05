# Implementation status

Last updated: 2026-08-05

This is the pause-and-resume snapshot for the project. The detailed product
scope and accepted decisions remain in [PRODUCT_FEATURES.md](PRODUCT_FEATURES.md);
the code and migrations remain the implementation source of truth.

## Read this first

**Where the project stands.** The application is a working multi-tenant garage
back office — customers, agenda, catalogue, team, internal assistant — and the
telephone agent now answers a call and saves the conversation. What it cannot
do is buy the number that call arrives on: provisioning has a data model, a
carrier adapter and no screen, so a number is bought by hand in the Twilio
console.

**What is waiting on someone else.** A Twilio regulatory bundle submitted on
2026-08-05 with a Martinique address, in review. Its verdict decides whether a
French number can be bought at all under this identity; the fallback, if it is
refused, is BYOC with a number from a carrier that serves the overseas
departments. Nothing else is blocked by it — see
[provider-decision.html](provider-decision.html).

**What the team owes before the first paying client.** VAT, settled with an
accountant, because the franchise en base written on the order form stops being
true well inside the first year — the sourced reading is in
[billing-model.md](billing-model.md). Then a Meta Business account for
WhatsApp, and a vehicle-data provider, neither of which this repo can choose.

**Where to pick the work back up.** The provisioning screen, which makes the
store and `internal/platform/twilio` reachable and turns the order form's
activation promise and the offer's clean exit into something the application
does. After that, transferring a caller to the workshop's landline: decided,
specified, not written.

## Repository state at pause

- All local work is merged into `main`; no local `staging`, backend, or
  frontend branches/worktrees remain.
- The working tree is clean, and `main` is pushed to `origin/main` — this
  record commit included.
- Two things to know before the first deployment. `BASE_URL` must match the
  public origin exactly, or every Twilio signature fails and the failure looks
  like the carrier's fault. And the test suite showed three intermittent HTTP
  failures under parallel load on 2026-08-05, green in isolation and on a
  rerun; if it recurs, it is container contention to fix in CI, not product
  code.

## Row-level security is only real under an unprivileged role

PostgreSQL exempts superusers and `BYPASSRLS` roles from row security, so a
deployment whose `DATABASE_URL` names a superuser runs with every policy in
`internal/platform/db/migrations` inert — tenant isolation included. The app
still behaves, because the stores scope their queries in Go too, but the
defence then exists once instead of twice.

`cmd/web` must therefore connect as a `NOSUPERUSER NOBYPASSRLS` role holding
only table privileges, while `cmd/migrate` keeps a privileged one. The SQL and
the one-line check that proves a deployment is protected are in the README
under "Database roles". Any new environment has to do this explicitly; nothing
in the schema can enforce it.

Test coverage makes the same distinction: `dbtest.OpenRuntime` gives stores a
connection that `SET ROLE`s to a non-superuser, and every feature that owns
tenant data now uses it. Only the OAuth-flow tests, which touch `users` and
`sessions` (neither carries row security), still open a privileged connection.

## Completed and verified

- PostgreSQL 18 is the sole application database. Native UUIDv7 identifiers,
  PostgreSQL constraints, forced row-level security, tenant/user transaction
  context, and Testcontainers-backed integration tests are in place.
- OAuth sessions, organization selection, multi-location organizations,
  location-scoped membership, location administration, and SIRET-assisted
  onboarding are implemented.
- Customer records support multiple vehicles, contacts, appointments, repair
  history, AI memories, per-location ownership, explicit cross-location
  sharing, revocation, and retained operational history.
- The customer UI provides location-safe search and a complete dossier. The
  agenda supports timezone-safe booking, service buffers, opening hours,
  closures, resource allocation, and database-enforced conflict prevention.
- The call inbox, ordered transcripts, and telephone-agent configuration UI
  are implemented. Actual telephone, speech, and model provider runtimes are
  deliberately not connected yet.
- The structured service/product/package catalog supports CSV and XLSX staging,
  validation, human-confirmed publication, replacement, rollback, location
  scope, price validity, and immutable audit history.
- The internal operations assistant stores private location-scoped
  conversations and immutable tool audits. It can safely:
  - read currently effective catalog prices;
  - find customers and vehicles by name, contact, plate, or VIN without leaking
    records from another location accessible to the same employee;
  - check appointment availability for a service and date, and list a day's
    appointments at the scoped location, reusing the agenda's own timezone,
    opening-hours, resource, and conflict rules (services needing manual
    resource selection return an explicit scope-boundary message instead of
    guessing a resource id);
  - preview a location contact change and apply it only after explicit human
    confirmation, with authorization, optimistic concurrency, and idempotent
    recovery;
  - preview and, after confirmation, book, reschedule, or cancel an
    appointment — reusing Store.Save/Store.Cancel's own database exclusion
    constraints and WHERE-clause guards for conflict detection and staleness,
    with idempotency receipts recorded in the same transaction as the write
    so a retried confirmation never double-books or double-cancels;
  - preview and, after confirmation, correct a customer's name/company name/
    email/phone or a vehicle's plate/make/model/VIN. Authorization is pure
    RLS (only the customer's home location can write; a location holding
    only a read grant gets an explicit forbidden message instead of a
    confusing failed write), optimistic concurrency reuses the same
    updated_at WHERE-clause guard as appointments, contact corrections
    supersede (soft-delete + insert) rather than overwrite so contact
    history stays intact, and constraint violations (duplicate contact,
    duplicate plate, bad format) are decoded via db.PgError instead of
    hand-rolled validation;
  - preview and, after confirmation, record a fact about a customer
    (`propose_customer_memory`) using the same synchronous chat
    preview/confirm pattern as every other write tool — no new page and no
    new `customer_memories.status` value; a confirmed memory is written
    directly as `active`. Any location that can already see the customer's
    dossier may record a memory for it (RLS only checks the memory's own
    location, not the customer's home location, unlike corrections), and a
    later proposal with the same key from the same location supersedes the
    earlier value in place (upsert on the table's own unique constraint)
    instead of accumulating duplicates;
  - list a location's bookable resources (`list_bookable_resources`) and
    name one when booking a service that needs manual resource selection
    (`book_appointment`/`check_availability` now accept `resource_ids`,
    wired straight into `Store.Save`/`Store.Availability`'s existing
    parameter — no new booking logic). A manual-allocation service booked
    without naming a resource is a normal, resolvable validation error now,
    not the earlier hard "unsupported" wall.
- Customer-profile controls for owners/admins: grant or revoke an individual
  dossier share with another site (`app_current_user_manages_tenant()` gates
  it, not home-location ownership — a staff member who can edit the dossier
  still cannot share or offboard it), inspect the full share history
  (nothing is ever deleted, only `revoked_at` set), and offboard a customer
  who has left. Offboarding soft-deletes the customer and their active
  contacts; the active-contact unique index is partial
  (`WHERE deleted_at IS NULL`), so the freed phone/email becomes assignable
  to a new customer automatically, with no separate release step.
- Provider-neutral ports exist for telephony, speech, LLMs, calendars, vehicle
  lookup, secrets, and business lookup. No permanent provider choice has been
  imposed.
- Google Calendar is connected end to end for the location-management UI:
  application-layer AES-256-GCM secret storage (`encrypted_secrets`, RLS
  manager-gated writes) never puts a refresh token in Postgres in the clear;
  `internal/platform/calendar` implements event upsert/delete and
  freebusy against the documented REST endpoints with no new dependency
  (`golang.org/x/oauth2` only, already in `go.mod`); and a manager can
  connect/disconnect a site's calendar from its edit page (OAuth2
  state+PKCE cookie flow mirroring the login provider, one active
  connection per location, reconnecting replaces rather than accumulates).
  The agenda now pushes: booking and rescheduling call
  `Store.SyncAppointmentCalendar` and cancelling calls
  `Store.RemoveAppointmentCalendarEvent` (`agenda/handler.go`), with sync
  state in `appointment_calendar_events`. Still one-way by design —
  Garageband stays the source of truth — so edits made directly in Google
  Calendar are not reconciled back, and no sync token is consumed.
- Staff who own no email account, password or Google login can now work in
  the app. An owner enrols someone from the team screen by name and sites
  alone; the store mints a twelve-character base32 code (60 bits, an
  alphabet with no O/0 or I/1 to mishear) whose SHA-256 hash is all the
  database keeps. The employee enters it at `/rejoindre` on any machine, in
  whatever case and dashes they type, or taps the same secret as a link.
  Previewing a link never consumes it, so a messenger's preview fetch cannot
  burn an employee's only way in; only the POST does. Owners reissue a code
  (retiring the previous one) for a second screen or a lost one, correct a
  name without touching access, and remove someone — which takes their
  pending code with it through the membership foreign key and clears their
  workspace without signing them out of another garage they belong to.
  Staff sessions last 90 days because their device is the credential.
- Appointment reminders are a staff workflow, not an automated one: an
  employee records the outcome of a call they placed themselves and the
  appointment drops off the reminder queue either way. Nothing dials.

At this pause point, `make generate`, the complete `make test` suite, `go vet
./...`, and `git diff --check` pass. Database tests run against PostgreSQL 18
through Testcontainers rather than silently skipping when a local database is
absent.

## Recommended next slices

1. Build one end-to-end inbound telephone tracer bullet with webhook
   verification, caller disambiguation, transcription, model/tool orchestration,
   voice output, fallback, and observability, while reusing the same customer,
   catalog, and scheduling tools. **The vendor decision is made**:
   [provider-decision.html](provider-decision.html) records Twilio alone, with
   ConversationRelay — numbers, voice routing, WhatsApp, portability, speech
   recognition, voice, and interruption handling from one provider, each site
   keeping its own fixed number routed to its own Garageband context.
   Garageband writes the agent loop itself: a WebSocket server that receives
   transcribed text, calls the model and the product's own tools, and returns
   what to say. That is the same orchestration the internal assistant already
   performs — tool registry, authorization, previews, confirmations, audits —
   so the phone gets one agent loop shared with the screen rather than a
   second one rented elsewhere. Renting it from Retell stays the fallback if
   the loop proves harder than expected.

   **Two pieces of it are built** (`internal/features/voice`). The loop itself:
   `POST /voice/incoming` answers with the TwiML that hands the audio to
   ConversationRelay, `GET /voice/relay` carries the socket, and `Session`
   holds one call's transcript — answering only the final transcription,
   keeping the spoken greeting in the history, and truncating an interrupted
   sentence to `utteranceUntilInterrupt` so the next answer never builds on
   words nobody heard. Neither endpoint sits behind `RequireTenant`, because a
   caller is anonymous: the webhook is Twilio-signed and the site travels in
   that signed URL rather than being looked up, and the socket carries a
   two-minute HMAC token minted in the TwiML. Missing `TWILIO_AUTH_TOKEN`
   registers no routes at all — that token is the key both proofs rest on. The
   responder is a fixed sentence on purpose, since no model may be connected
   before its tools, authorization, confirmations and audits are complete.

   And the provisioning state (migration 0031): `telephony_accounts` holds one
   subaccount per organization and refuses a second, `regulatory_bundles`
   records the compliance file with the submission and review timestamps that
   measure time to activation, and `phone_numbers` gained the bundle it was
   bought under, a WhatsApp sender flag, a `provisioning` status and a release
   that frees the E.164 for the next customer. `internal/platform/twilio`
   implements the provisioning port with the official SDK.

   A call now saves itself as it happens, into the `calls` and `call_messages`
   tables the inbox already reads. The runtime writes with a tenant and no
   user, since the caller is anonymous, which migration 0032 recognises with
   policies narrow enough that a user-less reader reaches its own conversation
   and no customer dossier — asserted by a test. Turns are written only once
   they can no longer change, because an interrupt truncates the agent's last
   sentence, and the closing writes run on a context detached from the socket,
   which is already cancelled by the time someone hangs up. `summary` and
   `outcome` stay empty: they are the model's work, and no model is connected.

   What is missing on this slice is the write path for provisioning: nothing in
   the application calls `AttachNumber`, `ActivateNumber` or `RecordBundle`,
   and `internal/platform/twilio` has no caller either, so a number is bought
   in the Twilio console rather than through Garageband. The screen that closes
   that gap is the next piece, and it is what makes the order form's activation
   promise and the offer's clean exit real rather than manual.

   What remains before the rest is an account and a real French number, not
   another comparison. The same document lists what must be proven on a live
   account before any contract: a metropolitan French number receiving a
   no-answer forward, including one forwarded from a real Martinique line,
   since Twilio sells no +596 and does not port one either; the fr-FR voice
   with human transfer and transcription — the transfer being plain TwiML on
   the same call now, with no SIP trunk to configure — a WhatsApp sender with
   opt-in and SMS fallback, full removal of a site followed by reassignment of
   a Garageband-owned number to another one with no call, message, or data
   leakage, and the real cost at 700 and 1 800 minutes.
2. Add the WhatsApp channel after the channel-neutral conversation/runtime
   foundation is proven. Preserve garage ownership of its Meta/WABA identity,
   use embedded onboarding where available, and keep provider-specific data out
   of the core domain. **Needs a Meta Business/WABA account set up by the
   team first** — compliance and identity ownership make this one to not
   provision unilaterally.
3. Select and integrate a French registration-plate/VIN data provider behind
   the existing vehicle lookup port, with confirmation and lookup audit.
   **Needs a vendor decision first**: this is a paid data provider choice
   with a contract, not a code decision.

Slices 2 and 3 still need a credential or vendor decision from the team before
their adapter code is worth writing — neither is a code decision this repo can
make unilaterally. Slice 1 no longer does.

## Still planned, not yet implemented

- Real telephony, STT, TTS, and LLM provider runtimes; the WhatsApp channel;
  vehicle-data adapters; and reconciliation of edits made directly in Google
  Calendar (one-way push from the agenda is implemented).
- Live call handling, recordings/retention workflow, automated reminders,
  no-show flows, human handoff, and cross-channel telephone/WhatsApp
  continuity. Transferring a caller to a person — "je veux parler à un
  mécanicien" — is unbuilt, but no longer unspecified: the target is the
  location's own landline, one transfer number per site, and the telephony
  port needs a transfer primitive it does not have. Staff phone numbers stay
  out of the schema on purpose (see PRODUCT_FEATURES.md), so the agent
  transfers to the garage rather than to a named mechanic, and an unanswered
  transfer becomes a message in the staff work queue instead of a lost call.
- Signing a staff device out without removing the person from the
  organization, and showing an owner whether and when someone signed in.
  Today revoking is all-or-nothing and the team screen only distinguishes
  "invitation pending" from "has joined".
- SMS fallback for short transactional notifications when a customer has no
  WhatsApp sender or has not opted in. `PRODUCT_FEATURES.md` now commits to it
  as a metered, recorded channel decision — consent, delivery status, country,
  segment count, and the location's remaining allowance are all checked before
  sending — and `pricing-model.md` prices per-plan SMS quotas. No code exists:
  no channel decision record, no allowance counter, no sender.
- The per-location number model the same revision describes: a public telephone
  route per location, an optional shared or per-location WhatsApp sender,
  explicit ownership of every number (customer-owned or Garageband-owned), and
  the exit flow that must complete its telephony and WhatsApp cleanup before a
  Garageband-owned number is released or reassigned.
- Duplicate-customer detection and merge, communication consent management,
  billing/subscriptions, retention/export/erasure workflows, operational
  monitoring, and production security runbooks.
- Additional intelligent document imports beyond the implemented CSV/XLSX
  catalog path, including a review-first extraction workflow for unstructured
  documents.

When work resumes, the telephone tracer bullet is the one slice whose vendors
are settled; WhatsApp and vehicle data still wait on the team (flagged inline).
The items above that touch neither — staff device sign-out and sign-in
visibility, human transfer, duplicate merge — are buildable today.
Keep each addition as a tested vertical slice. Do not connect a
real model before its permitted tools, authorization, confirmation rules,
and audits are complete.
