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
vehicle lookup, conversational messaging, secrets, and web search are
intentionally deferred. Product features depend on provider-neutral Go
interfaces.

## Accepted domain decisions

### Organization and physical locations

- The product-facing concept is an **organization**: the business operated by
  an owner. The internal database name is currently `tenant`.
- One organization can operate multiple physical garage **locations**.
- A location has its own SIRET, address, opening hours, services, bookable
  resources, appointments, repair work, AI agent, telephone number, provider
  connections, and calendar connections where applicable.
- A location is not a separate tenant. PostgreSQL RLS isolates organizations.
- Users join an organization through memberships. Owners and admins implicitly
  reach every location in their organization. Managers and members reach only
  the locations to which they are explicitly assigned, and a user may be
  assigned to more than one.

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
- Revoking a customer share immediately hides records authored by the source
  location and all future source updates from the receiving location.
- Revocation never removes the receiving location's access to appointments,
  repairs, invoices, or notes it authored. It retains only the minimum customer
  and vehicle identity required to understand and preserve those operational
  and legal records.
- The source location retains read-only visibility of appointments and repairs
  that the receiving location created while the share was active. Activity
  created by the receiving location after revocation is not shared back.
- While a share is active, it automatically includes future vehicles,
  appointments, repairs, and memories added to the living customer dossier; it
  is not a snapshot taken at grant time.

### WhatsApp conversational channel

- WhatsApp is an optional post-onboarding capability and never blocks a garage
  from starting to use Garageband while provider or Meta approval is pending.
- Each organization has one dedicated public AI number for both telephone calls
  and WhatsApp when the selected number and provider support both capabilities.
  The garage keeps any pre-existing WhatsApp Business number and application.
- A multi-location organization shares this public number. Before answering a
  location-dependent question about prices, services, availability, or booking,
  the agent requires the customer to choose the relevant location.
- Customer-facing confirmations always identify the selected location and
  include its useful details, such as address and appointment time.
- Automated WhatsApp replies are available 24 hours a day by default. An
  organization can configure different behavior and human-response promises per
  location, including outside opening hours.
- The V1 supports inbound conversations and transactional outbound messages:
  appointment confirmations, reminders, changes, cancellations, requested
  follow-ups, and information needed to complete those operations. Promotional
  campaigns are out of scope for V1.
- Outbound confirmations and reminders that require WhatsApp opt-in use an
  explicit, non-preselected consent. The consent text, response, source, and
  timestamp are retained as evidence, and opt-out is respected.
- The V1 accepts text and private JPEG/PNG attachments. Images may help extract
  a registration plate, but the customer must confirm the extracted value. The
  agent does not diagnose damage or create an estimate from an image. Automated
  document, audio, and video processing are deferred.
- The same verified customer may continue context between a telephone call and
  WhatsApp on a best-effort basis. When identity is ambiguous, the agent starts
  a fresh conversation instead of guessing. A name and registration plate are
  required before using existing customer history, especially when a telephone
  number is shared by multiple people.
- Low-risk facts explicitly stated by a customer, such as a preferred callback
  period, may be saved directly to the customer profile with their exact
  conversation provenance. Identity, contact, address, and vehicle changes
  require explicit confirmation; inferred or ambiguous facts require human
  review. Consent and financial commitments are never stored as ordinary agent
  memories.
- When the agent cannot safely complete a request, it collects the customer's
  name, reason, and telephone number, pauses automated replies, and assigns the
  conversation to the selected location's Garageband inbox. The promised human
  response delay is configurable per location. Owners and admins may supervise
  every location; other users remain limited by their location assignments.
- The garage owns its Meta business identity, WhatsApp Business Account, sender
  identity, and reusable messaging assets. Garageband operates them through a
  provider integration and must support an exit path without provider lock-in.
  Embedded provider onboarding should reuse SIRET, business, location, website,
  and logo data already collected by Garageband to minimize manual input.
- Garageband owns the channel-neutral conversation, message, consent,
  attachment, assignment, tool-execution, and audit records. A provider such as
  Twilio is an adapter and must not define the application data model.

## Implemented

### PostgreSQL foundation

- PostgreSQL 18 is the only application database; application SQLite code and
  the boilerplate todo feature were removed.
- Native `UUID` primary keys use PostgreSQL `uuidv7()` defaults.
- Native `TIMESTAMPTZ`, `JSONB`, range types, checks, composite foreign keys,
  partial indexes, generated ranges, and exclusion constraints are used for
  database-level validation.
- Goose migrations are embedded and PostgreSQL-only.
- The default test command provisions an ephemeral PostgreSQL 18 instance with
  Testcontainers; `TEST_DATABASE_URL` can reuse an existing instance.
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

### Location access and customer sharing foundation

- Owners and admins implicitly reach every location; managers and members use
  explicit, revocable `user_location_assignments`.
- The authenticated `/team` screen lets owners and admins atomically replace a
  manager or member's complete location assignment set. Other roles see only
  their own access in a read-only presentation.
- Assignment history is immutable: PostgreSQL records the assigning and
  revoking users and timestamps, and re-assignment creates a new audit row.
- Every customer has a required home location. Vehicles, vehicle lookups, and
  agent memories also carry the location needed for authorization.
- Owners and admins can create and revoke temporal customer-location grants
  through the access-control store. The customer-sharing UI remains planned.
- Forced PostgreSQL RLS applies location access and customer dossier grants to
  customers, contacts, vehicles, calls, memories, appointments, repairs,
  calendars, agents, phone numbers, services, and resources.
- A receiving site can read the living canonical dossier while a grant is
  active but cannot mutate common customer identity or vehicles owned by the
  home site.
- Appointments and repair orders preserve immutable customer and vehicle JSONB
  identity snapshots; customer memories preserve an immutable customer
  snapshot. After revocation, each site retains only its own records and the
  source keeps read-only visibility of receiving-site activity created during
  the grant interval.
- Database and HTTP tests cover assignment replacement, forbidden targets,
  grant/revoke/regrant audit history, active dossier access, canonical write
  rejection, revocation, retained records, and historical source visibility.

### Customer search and dossier

- Authenticated `/customers` search by name, normalized French telephone,
  e-mail, registration plate with or without hyphens, and VIN.
- PostgreSQL-generated search columns, trigram indexing, compact-plate indexing,
  existing contact/VIN indexes, soft-delete filtering, deterministic ordering,
  and a bounded result set.
- Search results include primary contacts, the vehicle fleet, owning location,
  and whether access comes through a customer-location grant.
- Authenticated customer profiles combine every visible vehicle, appointment,
  repair, and agent memory into an RLS-filtered dossier and ordered timeline.
- An actor may edit canonical customer data only when their role or assignment
  reaches the home location. Events from other sites remain visibly read-only.
- Source and authoring location names and referenced appointment services are
  exposed only when the corresponding shared dossier event is itself visible.
- Integration and HTTP tests execute the store as a non-superuser and cover all
  search keys, profile composition, grant activation, revocation, and retained
  source visibility of receiving-site activity.

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

### Day agenda and appointment booking

- Authenticated day agenda for each accessible physical location, with explicit
  location selection preserved through day navigation and booking flows.
- Appointment creation, editing, and cancellation for a customer and one of
  their vehicles.
- Location-timezone conversion on reads and location-timezone parsing on writes
  prevents UTC shifts from changing workshop bookings.
- Appointment duration is derived from the selected service duration and its
  buffers rather than trusted from browser input.
- The booking form searches 15-minute candidates inside location opening
  hours, removing exceptional closures and active bookings for the selected
  resource without mutating the agenda.
- PostgreSQL prevents overlapping weekly opening windows, bookings outside a
  configured schedule, bookings during closures, and closures over active
  bookings; weekdays without a window are closed once a schedule exists.
- Active services, active resources, customer/vehicle ownership, membership,
  and location access are revalidated inside the tenant/user transaction.
- PostgreSQL exclusion violations are rendered as slot conflicts rather than
  malformed-input errors; cancelled appointments remain visible but stop
  occupying capacity.
- Testcontainers integration tests cover local-time persistence, service
  buffers, concurrent-slot rejection, HTTP conflict presentation, and
  cancellation.

### Call inbox and transcripts

- Authenticated call inbox across the actor's accessible locations, newest
  first, with an attention filter using the same recognition/answer rule as the
  views.
- Caller numbers are formatted for display, customer links exist only while
  the customer dossier is visible, and every timestamp is converted using the
  call location's timezone.
- Transcript messages are loaded strictly by their durable `sequence`; equal or
  non-monotonic event timestamps cannot scramble speech and tool activity.
- Missed calls remain visible without pretending they contain a transcript;
  recording presence is shown without exposing playback before the retention
  workflow exists.
- Runtime-role tests cover location-aware RLS, attention filtering, telephone
  formatting, timezone conversion, and transcript order.

### Telephone-agent configuration

- PostgreSQL creates exactly one draft telephone agent for every new location
  and backfills existing locations. A unique constraint makes the rule durable.
- Provider foreign keys enforce that selected LLM, speech-to-text, and
  text-to-speech connections belong to the agent's location and have the
  correct provider kind.
- Owners and admins can edit the name, greeting, fallback, instructions,
  language, and provider selections; assigned managers and members have
  read-only access.
- Copy can be prepared before providers exist, but activation independently
  verifies three selected active provider connections. Pause is a separate
  consequential action.
- The list distinguishes missing providers, missing telephone routing, paused
  agents, and agents that callers can actually reach.
- PostgreSQL length constraints mirror handler validation for agent-controlled
  text, and tests cover automatic provisioning, uniqueness, cross-kind
  rejection, role enforcement, readiness, activation, pause, and reachability.

### Structured offer catalog and imports

- Organization catalog for services, products, packages, supplements, and
  labor rates, with fixed, starting, range, and quote-only prices.
- Integer-cent EUR amounts, explicit HT/TTC basis, VAT basis points, optional
  duration, effective dates, references, and all-site or selected-site scope.
- Owners and admins manage the catalog; other members have RLS-filtered,
  read-only access based on their location assignments.
- CSV and XLSX uploads are limited to 5 MiB, checksummed, retained with source
  metadata and uploader provenance, and idempotent per location.
- Every parsed row is staged as valid, ambiguous, or rejected. No upload writes
  directly into the published catalog.
- Human-confirmed merge and replace plans share their predicates with the
  publication transaction. Replace archives only absent single-location items
  and cannot silently alter shared multi-location prices.
- Each publication has an organization version plus immutable before/after
  snapshots. The latest unchanged publication can be rolled back safely.
- `Store.Quotable` is the shared agent read contract and returns only currently
  effective prices for an accessible selected location.
- PostgreSQL constraints, deferred scope triggers, immutable audit triggers,
  and forced RLS enforce the catalog contract independently from HTTP code.

## In progress

- No partially implemented vertical slice is currently tracked. Scheduling
  availability and provider integrations remain planned as separate slices.

## Planned

### Location authorization and customer sharing

- Add customer-profile controls for owners and admins to grant or revoke access
  for individual receiving locations.
- Show current access, source and receiving locations, actor, grant time,
  revocation time, and historical grants without allowing audit-row edits.

### Customer relationship management

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

- Owner/admin screens for weekly opening hours and exceptional closures.
- Multi-resource allocation across technician, bay, and equipment requirements.
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

### WhatsApp agent runtime

- Define a provider-neutral conversational-messaging port before adding a
  Twilio adapter.
- Model channel-neutral conversations so telephone and WhatsApp interactions
  can share a verified customer context without merging ambiguous identities.
- Add organization-level sender configuration with per-location routing,
  business-hour behavior, response promises, and human inbox assignment.
- Implement signed inbound webhooks, idempotent message ingestion, delivery
  status reconciliation, ordered message history, retries, and dead-letter
  handling.
- Implement explicit WhatsApp consent and opt-out records independently from
  ordinary customer memories.
- Add private attachment ingestion with size/type validation, checksums,
  location-aware access control, configurable retention, and deletion.
- Add approved transactional-template management and prohibit promotional sends
  in the V1 application tools.
- Pause automation during human ownership and require an explicit release before
  the agent resumes the conversation.
- Prove one real French tracer bullet before promising the combined number:
  provision a dedicated number, receive voice and WhatsApp traffic, configure
  the business profile, send a consented template, and verify the documented
  provider-exit path.
- Open provider questions include French number inventory and regulatory
  eligibility, combined voice/WhatsApp capability, Meta and provider approval
  lead times, pricing, sender migration, and number portability.

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

### Additional intelligent import formats

- Add less structured PDF or office-document ingestion only after selecting a
  parser/provider and defining malware scanning and retention.
- Add a user-controlled column-mapping step for exports whose headings are not
  among the supported French and English aliases.

### Organization administration

- Invite users and manage membership roles.
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

1. **Complete:** location assignments and customer access grants were built
   before customer CRUD so authorization is not retrofitted later.
2. **Complete:** customer and vehicle profiles now provide search and a
   repair/appointment timeline under location-aware RLS.
3. **Complete:** the structured offers/products/packages catalog, CSV/XLSX
   staging, confirmed publication, safe rollback, and quotable-price contract
   exist before an agent is allowed to answer pricing questions.
4. **Partially complete:** appointment CRUD, working-time enforcement, closures,
   and single-resource availability search are implemented; add schedule
   administration and multi-resource allocation before an external calendar.
5. Implement the internal operations chat using explicit, authorized tools and
   fake model adapters.
6. Add Google Calendar synchronization.
7. Implement one end-to-end telephone provider tracer bullet reusing the same
   customer, catalog, and scheduling tools with fake LLM and speech adapters.
8. Add real voice/model providers only after call and tool contracts are proven.
9. Add WhatsApp after a French-number provider tracer bullet proves combined
   voice/messaging capability and sender portability; reuse the same customer,
   catalog, scheduling, and authorized-action tools.
