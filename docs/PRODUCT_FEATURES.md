# Garageband product and feature ledger

Last updated: 2026-08-01

This document is the durable product reference for humans and agents. Update it
when a feature is accepted, implemented, deferred, or materially redesigned.
Code and migrations remain the source of truth for implementation details.

## Status legend

- **Implemented**: present in the repository and verified.
- **In progress**: code exists but the slice is not fully verified or finished.
- **Planned**: accepted product scope without an implementation yet.
- **Open decision**: implementation must wait for a product decision.
- **Suggested**: useful candidate that has not been accepted as scope.

## Product definition

Garageband is a multi-tenant SaaS for automotive repair businesses. It provides
AI telephone agents that can answer calls, recognize customers, retain useful
customer information, look up vehicles, and schedule garage appointments.

Provider choices for telephony, speech-to-text, text-to-speech, LLMs, calendar,
vehicle lookup, secrets, and web search are intentionally deferred. Product
features depend on provider-neutral Go interfaces.

## Accepted domain decisions

### Organization and physical locations

- The product-facing concept is an **organization**: the business operated by
  an owner. The internal database name is currently `tenant`.
- One organization can operate multiple physical garage **locations**.
- A location has its own SIRET, address, opening hours, services, bookable
  resources, appointments, repair work, AI agent, telephone number, provider
  connections, and calendar connections where applicable.
- A location is not a separate tenant. PostgreSQL RLS isolates organizations.
- Users join an organization through memberships. More granular staff access
  to locations is still to be designed.

### Customer ownership and cross-location sharing

- A customer is private by default to the location where the record was
  created.
- The customer is not automatically visible to every location in the same
  organization.
- An authorized owner can explicitly share an individual customer with another
  location.
- Sharing covers the complete customer dossier, not only contact information:
  identity, contacts, every vehicle, appointments, repair history, and memories
  saved by the telephone agent.
- No records are copied when shared. Locations receive access to the same
  canonical customer dossier.
- A receiving location can read the shared common dossier but cannot directly
  modify the customer's common identity, vehicles, or records authored by
  another location.
- A receiving location can create and update only its own appointments, repair
  records, and location-authored notes or memories. Those records become part
  of the shared dossier while preserving their authoring location.
- Organization owners and admins can grant or revoke customer sharing between
  locations. Every change records the actor, source location, receiving
  location, and timestamp in the access-control audit trail.

### Open decisions for customer sharing

The following remain open:

- Which employees are assigned to each location and whether owners implicitly
  access every location.
- What revocation hides when the receiving location has already created an
  appointment or repair order for the shared customer.
- Whether sharing always applies to future vehicles and history automatically.
  The current assumption is yes because the accepted scope is the complete
  customer dossier.

## Implemented

### PostgreSQL foundation

- PostgreSQL 18 is the only application database; application SQLite code and
  the boilerplate todo feature were removed.
- Native `UUID` primary keys use PostgreSQL `uuidv7()` defaults.
- Native `TIMESTAMPTZ`, `JSONB`, range types, checks, composite foreign keys,
  partial indexes, generated ranges, and exclusion constraints are used for
  database-level validation.
- Goose migrations are embedded and PostgreSQL-only.
- PostgreSQL integration tests create isolated schemas.
- Runtime-role tests prove forced RLS instead of accidentally testing as a
  superuser.
- Tenant database access uses transaction-local
  `app.current_tenant_id` through `db.WithinTenant` so pooled connections cannot
  leak tenant context.

### Authentication

- OAuth provider interface and Google adapter.
- OAuth state validation and PKCE.
- Server-side sessions store only SHA-256 token hashes.
- Session expiry is enforced server-side and logout revokes the session.
- Authentication middleware exposes the current user and protects private
  routes.
- Each session independently stores its active organization.
- A composite foreign key guarantees that the active organization belongs to
  the session user and clears it when the membership is deleted.
- Transaction-local user context and forced RLS let a user discover only their
  own organizations before selecting one.
- Users can switch organizations from the dashboard, and onboarding activates
  the newly created organization automatically.
- `auth.TenantFrom` and `auth.RequireTenant` provide the trusted organization
  context for tenant-owned feature routes.
- Integration tests cover non-member activation, per-session isolation,
  membership deletion, and RLS enforcement under a non-superuser runtime role.

### Organization tenancy schema

- Organizations (`tenants`) with stable slug, legal identity, locale, and
  lifecycle status.
- Organization memberships with owner, admin, manager, and member roles.
- Multiple physical locations per organization.
- Location contact details, timezone, lifecycle status, and opening hours.
- Business enrichment audit records.
- Forced RLS on all organization-owned tables.

### Physical location management

- Authenticated `/locations` list, add, edit, deactivate, and reopen flows.
- Owners and organization admins can manage locations; managers and members
  receive a read-only presentation.
- Locations are durable records and cannot be hard-deleted through the runtime
  PostgreSQL role.
- PostgreSQL RLS independently verifies both the active organization and the
  authenticated user's membership role for reads and writes.
- The database validates SIRET, normalized email, E.164 telephone numbers,
  website URLs, country codes, lifecycle status, and real PostgreSQL timezone
  names.
- UUIDv7-backed internal location slugs are generated without requiring users
  to invent another identifier.
- PostgreSQL updates `updated_at` automatically for every location mutation.
- Responsive DaisyUI views cover single-site, multi-site, inactive, read-only,
  validation, loading, success, and failure states.
- HTTP, store, database-validation, cross-tenant, and adversarial runtime-role
  tests cover the complete slice.

### Business onboarding

- Authenticated SIRET onboarding flow.
- Official French Recherche d'entreprises API adapter.
- Exact establishment matching, inactive-establishment rejection, bounded HTTP
  response handling, and provider-neutral business lookup interface.
- User-owned, expiring pre-tenant onboarding drafts.
- Editable confirmation of business and first-location information; SIRET
  remains bound to the registry lookup.
- Atomic creation of organization, owner membership, first location, and
  enrichment audit inside a newly established tenant RLS transaction.
- Idempotent confirmation retries.
- Pre-tenant registry payload is scrubbed after transfer to the tenant audit.
- Cross-user draft confirmation is rejected.
- DaisyUI/Tailwind onboarding pages include responsive forms, validation,
  loading behavior, and recoverable error states.

### Customer, vehicle, appointment, and repair schema

- Customer identities and normalized phone/email contacts.
- Multiple vehicles per customer.
- Registration plate and VIN validation and uniqueness.
- Auditable vehicle lookup runs.
- Location-specific services and prices.
- Bookable technicians, bays, equipment, and calendars.
- Location-specific appointments linked to customer, vehicle, service, and
  resource.
- PostgreSQL exclusion constraint prevents resource double-booking.
- Durable repair orders and repair line items, separate from planned
  appointments.
- Repair history supports multiple vehicles and multiple repairs per customer.

Important limitation: customers are currently organization-scoped in the
schema. The accepted default-location ownership and explicit sharing model is
not implemented yet.

### AI telephone-agent schema and provider ports

- Provider-neutral interfaces exist for telephony, LLM, speech, calendar,
  secrets, business lookup, and vehicle lookup.
- Location-specific agent configuration and provider connections.
- Location telephone numbers bound to agents.
- Auditable inbound and outbound calls.
- Ordered call messages/transcripts.
- Idempotent tool execution records.
- Structured customer memories with confidence and review status.
- Call recording fields enforce purpose, notification, and retention
  consistency.
- Appointment-to-external-calendar synchronization records.

These are database contracts and ports, not working provider integrations.

## In progress

No implementation slice is currently open.

## Planned

### Location authorization and customer sharing

- Add default/home location ownership to customer records.
- Add user-to-location assignments for non-owner employees.
- Add explicit customer-to-location access grants.
- Enforce access in PostgreSQL as defense in depth, in addition to explicit
  authorization in application queries.
- Make vehicle, appointment, repair-history, and agent-memory visibility follow
  the accepted complete-dossier sharing rule.
- Record who shared a customer, when, and with which location.
- Support safe access revocation after the open product questions are settled.

### Customer relationship management

- Customer search by normalized phone, email, name, plate, and VIN.
- Customer profile with multiple vehicles and complete repair timeline.
- Duplicate detection and deliberate merge workflow.
- Human review, correction, rejection, and provenance for memories proposed by
  the telephone agent.
- Customer communication preferences and consent state.

### Vehicle intelligence

- Select a French plate/VIN provider.
- Implement its adapter behind `vehiclelookup.Provider`.
- Plate normalization and lookup from calls and dashboard.
- User confirmation when provider data is ambiguous.
- Cache/provider audit and controlled refresh.

### Scheduling and calendars

- Appointment CRUD and availability search.
- Opening-hours, service-duration, buffer, technician, bay, and equipment
  constraints.
- Google Calendar OAuth connection and synchronization.
- Idempotent external event creation and reconciliation.
- Rescheduling, cancellation, no-show, reminders, and confirmation workflows.
- Define conflict ownership between Garageband and external calendars.

### AI telephone agent runtime

- Select telephony, STT, TTS, and LLM providers.
- Implement adapters behind existing platform interfaces.
- Inbound call webhook verification and idempotency.
- Caller recognition and safe customer disambiguation.
- Tool calls for customer lookup, memory proposals, vehicle lookup,
  availability, appointment creation, rescheduling, and cancellation.
- Human transfer and fallback behavior.
- Per-location prompt, greeting, language, voice, business hours, and escalation
  configuration.
- Call summaries, outcomes, searchable transcripts, and quality review.
- Explicit confirmation before consequential tool actions.

### Garage operations assistant

- Add an embedded conversational assistant for garage employees.
- Scope every conversation and tool execution to the active organization and,
  where applicable, the active physical location.
- Reuse the same explicit application tools for the internal chat and the
  telephone agent; neither agent receives raw SQL or unrestricted database
  access.
- Let authorized users ask the assistant to find information and propose or
  perform actions such as correcting customer details, updating operational
  information, or managing appointments.
- Authorize every tool independently from the user's membership role and
  location assignments. Being allowed to use the chat does not imply being
  allowed to perform every action.
- Require preview and explicit confirmation for consequential or destructive
  actions, with stricter rules still to be decided per tool.
- Persist the requesting user, conversation, tool name, validated arguments,
  affected records, outcome, and timestamps in an immutable audit trail.
- Make tool execution idempotent so retries cannot create duplicate changes.
- Provide a human-readable explanation of what changed and a recoverable error
  when an action is rejected.

### Offers, products, packages, and intelligent imports

- Let each organization maintain products, services, fixed-price packages,
  optional extras, labor rates, descriptions, taxes, effective dates, and
  location availability.
- Allow CSV and XLSX uploads, plus less structured documents such as PDF or
  office documents when a parser/provider is selected.
- Store every upload and parsing run as an auditable import batch with source,
  checksum, status, errors, and provenance.
- Parse imported content into a staging area rather than writing directly to
  the published catalog.
- Validate normalized rows with PostgreSQL constraints, show ambiguities and
  rejected rows, and require a human preview/approval before publication.
- Support idempotent re-imports, versioned publication, rollback, and explicit
  replacement versus merge behavior.
- Let the telephone agent and internal assistant answer price and offer
  questions only from currently published catalog data, including whether a
  price is fixed, estimated, location-specific, tax-inclusive, or conditional.
- Never invent a price when the catalog is missing or ambiguous; the agent must
  explain the uncertainty and offer a human follow-up.
- Open decisions include document retention, maximum file size, accepted
  formats, tax representation, price ranges, and who may approve publication.

### Organization administration

- Invite users and manage membership roles.
- Assign staff to one or more locations.
- Configure opening hours, holidays, services, prices, bays, technicians, and
  equipment per location.
- Switch active organization and active working location.
- Audit administrative and access-control changes.

### Compliance and operations

- Finalize French call-recording notice, lawful-purpose, access, deletion, and
  retention workflows.
- Data export and deletion workflows.
- Secrets-provider implementation and credential rotation.
- Provider webhook replay protection and audit trail.
- Background jobs, retries, dead-letter handling, and operational alerts.
- Rate limiting for authentication, onboarding registry calls, and public
  webhooks.
- Backup, restore, observability, and incident procedures.

## Suggested product features

These are recommendations, not accepted scope:

- SMS/email appointment confirmations and reminders.
- Missed-call recovery and automatic callback queue.
- Estimate approval by secure link.
- Maintenance reminders based on date or mileage.
- Customer satisfaction follow-up after repair.
- Reception inbox for calls needing human action.
- Daily agenda and unresolved-call summary for each location.
- Import from existing garage-management software.
- Billing and subscription management per organization or per location.

## Recommended implementation order

1. Resolve the remaining customer-sharing questions.
2. Implement location assignments and customer access grants before customer
   CRUD so authorization is not retrofitted later.
3. Build customer and vehicle profiles with search and repair timeline.
4. Build the structured offers/products/packages catalog and CSV/XLSX staging
   import before an agent is allowed to answer pricing questions.
5. Build scheduling and availability without an external calendar first.
6. Implement the internal operations chat using explicit, authorized tools and
   fake model adapters.
7. Add Google Calendar synchronization.
8. Implement one end-to-end telephone provider tracer bullet reusing the same
   customer, catalog, and scheduling tools with fake LLM and speech adapters.
9. Add real voice/model providers only after call and tool contracts are proven.
