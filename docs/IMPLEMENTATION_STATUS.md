# Implementation status

Last updated: 2026-08-03

This is the pause-and-resume snapshot for the project. The detailed product
scope and accepted decisions remain in [PRODUCT_FEATURES.md](PRODUCT_FEATURES.md);
the code and migrations remain the implementation source of truth.

## Repository state at pause

- All local work is merged into `main`; no local `staging`, backend, or
  frontend branches/worktrees remain.
- The working tree is clean at `0ed3751`. Note that `main` is ahead of the
  last pushed state recorded here (`0930a56`).

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
   catalog, and scheduling tools. **Needs a vendor decision first**: which
   telephony provider (a real phone number, real cost) and which STT/TTS
   provider, before any adapter code is worth writing against a live account.
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

All three need a vendor or credential decision from the team before their
adapter code is worth writing — none is a code decision this repo can make
unilaterally.

## Still planned, not yet implemented

- Real telephony, STT, TTS, and LLM provider runtimes; the WhatsApp channel;
  vehicle-data adapters; and reconciliation of edits made directly in Google
  Calendar (one-way push from the agenda is implemented).
- Live call handling, recordings/retention workflow, automated reminders,
  no-show flows, human handoff, and cross-channel telephone/WhatsApp
  continuity. Transferring a caller to a person — "je veux parler à un
  mécanicien" — is unbuilt and unspecified: the app knows staff as accounts
  and as bookable resources, but holds no phone number for a human, and the
  telephony port has no transfer primitive.
- Signing a staff device out without removing the person from the
  organization, and showing an owner whether and when someone signed in.
  Today revoking is all-or-nothing and the team screen only distinguishes
  "invitation pending" from "has joined".
- Duplicate-customer detection and merge, communication consent management,
  billing/subscriptions, retention/export/erasure workflows, operational
  monitoring, and production security runbooks.
- Additional intelligent document imports beyond the implemented CSV/XLSX
  catalog path, including a review-first extraction workflow for unstructured
  documents.

When work resumes, all four remaining slices need a vendor or credential
decision from the team first (flagged inline) — none is buildable further
without one. Keep each addition as a tested vertical slice. Do not connect a
real model before its permitted tools, authorization, confirmation rules,
and audits are complete.
