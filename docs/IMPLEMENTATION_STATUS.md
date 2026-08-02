# Implementation status

Last updated: 2026-08-02

This is the pause-and-resume snapshot for the project. The detailed product
scope and accepted decisions remain in [PRODUCT_FEATURES.md](PRODUCT_FEATURES.md);
the code and migrations remain the implementation source of truth.

## Repository state at pause

- All local work is merged into `main`; no local `staging`, backend, or
  frontend branches/worktrees remain.
- The working tree is clean and the validated `main` branch is ahead of
  `origin/main` by 41 commits.
- No push has been performed. Pushing `main` to the remote is the next Git
  operation when the team is ready.

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
    recovery.
- Provider-neutral ports exist for telephony, speech, LLMs, calendars, vehicle
  lookup, secrets, and business lookup. No permanent provider choice has been
  imposed.

At this pause point, `make generate`, the complete `make test` suite, `go vet
./...`, and `git diff --check` pass. Database tests run against PostgreSQL 18
through Testcontainers rather than silently skipping when a local database is
absent.

## Recommended next slices

1. Add confirmed appointment creation, rescheduling, and cancellation through
   the same authorization, preview, concurrency, audit, and idempotency
   contracts used by the existing consequential assistant action.
2. Add confirmed customer/contact/vehicle corrections and a human review flow
   for AI-proposed customer memories.
3. Add customer-profile controls for owners/admins to grant and revoke an
   individual dossier share and inspect its immutable history.
4. Connect Google Calendar through the existing provider boundary, including
   idempotent event synchronization, reconciliation, and an explicit conflict
   ownership policy.
5. Build one end-to-end inbound telephone tracer bullet with webhook
   verification, caller disambiguation, transcription, model/tool orchestration,
   voice output, fallback, and observability, while reusing the same customer,
   catalog, and scheduling tools.
6. Add the WhatsApp channel after the channel-neutral conversation/runtime
   foundation is proven. Preserve garage ownership of its Meta/WABA identity,
   use embedded onboarding where available, and keep provider-specific data out
   of the core domain.
7. Select and integrate a French registration-plate/VIN data provider behind
   the existing vehicle lookup port, with confirmation and lookup audit.
8. Give the assistant a way to name a bookable resource for services that
   need manual resource selection (today it can only check availability for
   auto-allocated services — see `agenda.mapAvailabilityError`).

## Still planned, not yet implemented

- Real telephony, STT, TTS, LLM, Google Calendar, WhatsApp, and vehicle-data
  adapters.
- Live call handling, recordings/retention workflow, reminders, no-show flows,
  human handoff, and cross-channel telephone/WhatsApp continuity.
- Duplicate-customer detection and merge, communication consent management,
  billing/subscriptions, retention/export/erasure workflows, operational
  monitoring, and production security runbooks.
- Additional intelligent document imports beyond the implemented CSV/XLSX
  catalog path, including a review-first extraction workflow for unstructured
  documents.

When work resumes, start with item 1 and keep each addition as a tested vertical
slice. Do not connect a real model before its permitted tools, authorization,
confirmation rules, and audits are complete.
