# Implementation status

Last updated: 2026-08-02

This is the pause-and-resume snapshot for the project. The detailed product
scope and accepted decisions remain in [PRODUCT_FEATURES.md](PRODUCT_FEATURES.md);
the code and migrations remain the implementation source of truth.

## Repository state at pause

- All local work is merged into `main`; no local `staging`, backend, or
  frontend branches/worktrees remain.
- The working tree is clean and `main` is pushed and up to date with
  `origin/main` (`0930a56`).

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
  Not yet wired: pushing appointment writes to the connected calendar
  (agenda's save/cancel path) and sync-token-based reconciliation of
  edits/deletes made directly in Google Calendar — Garageband is meant to
  stay the source of truth (one-way push), but that policy isn't encoded
  in code yet since nothing pushes.

At this pause point, `make generate`, the complete `make test` suite, `go vet
./...`, and `git diff --check` pass. Database tests run against PostgreSQL 18
through Testcontainers rather than silently skipping when a local database is
absent.

## Recommended next slices

1. Wire appointment save/cancel in `internal/features/agenda` to push to a
   location's connected Google Calendar (`calendar.NewGoogle` + the location's
   decrypted refresh token), record sync state (`appointment_calendar_events`),
   and add sync-token-based reconciliation. No vendor decision left — the
   OAuth client, encrypted secret storage, and connect/disconnect UI are
   already built and tested; this is now a code-only slice.
2. Build one end-to-end inbound telephone tracer bullet with webhook
   verification, caller disambiguation, transcription, model/tool orchestration,
   voice output, fallback, and observability, while reusing the same customer,
   catalog, and scheduling tools. **Needs a vendor decision first**: which
   telephony provider (a real phone number, real cost) and which STT/TTS
   provider, before any adapter code is worth writing against a live account.
3. Add the WhatsApp channel after the channel-neutral conversation/runtime
   foundation is proven. Preserve garage ownership of its Meta/WABA identity,
   use embedded onboarding where available, and keep provider-specific data out
   of the core domain. **Needs a Meta Business/WABA account set up by the
   team first** — compliance and identity ownership make this one to not
   provision unilaterally.
4. Select and integrate a French registration-plate/VIN data provider behind
   the existing vehicle lookup port, with confirmation and lookup audit.
   **Needs a vendor decision first**: this is a paid data provider choice
   with a contract, not a code decision.

Slices 2–4 need a vendor or credential decision from the team before their
adapter code is worth writing — none is a code decision this repo can make
unilaterally. Slice 1 (agenda → Google Calendar push) is code-only and can
start any time.

## Still planned, not yet implemented

- Real telephony, STT, TTS, and LLM provider runtimes; the WhatsApp channel;
  vehicle-data adapters; and Google Calendar push/reconciliation from the
  agenda (connect/disconnect itself is already implemented).
- Live call handling, recordings/retention workflow, reminders, no-show flows,
  human handoff, and cross-channel telephone/WhatsApp continuity.
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
