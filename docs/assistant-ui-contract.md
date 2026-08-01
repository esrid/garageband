# Operations assistant UI and execution contract

The `assistant` feature is an employee-facing conversation surface. It does not
receive a database handle, raw SQL tool, tenant id, user id, or location id from
model output. The application resolves those values from the authenticated
session and conversation, then supplies them as a trusted `assistanttools.Scope`.

## Routes

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/assistant` | Start a conversation or load one owned by the current employee. |
| `POST` | `/assistant/messages` | Append one employee message and ask the injected model adapter for a response or tool proposal. |
| `POST` | `/assistant/{conversationID}/tools/{executionID}/confirm` | Confirm and idempotently execute one pending action. |
| `POST` | `/assistant/{conversationID}/tools/{executionID}/reject` | Reject a pending action without changing domain data. |

Every route requires an authenticated user and active organization. New
conversations require an active location visible through PostgreSQL RLS. A
conversation is private to its creating employee and permanently scoped to one
location; another employee cannot read it merely because they can access the
same location.

## Model and tool boundary

The model receives conversation text plus explicit JSON-schema tool
definitions. Feature-owned stores implement those tools behind the neutral
`internal/platform/assistanttools` registry, so the assistant does not import
another feature. The same executor contract and generic idempotency receipts can
later be reused by telephone and messaging orchestrators.

The initial adapter is deliberately local and provider-neutral. It recognizes
only explicit requests to change the selected location's email, E.164 phone, or
HTTP(S) website. It is labelled as demonstration mode in the UI and does not
pretend to answer arbitrary questions.

The first tool, `update_location_contact`, is owned by `locations`. It rejects
unknown JSON fields, normalizes and validates values, authorizes owner/admin
roles inside the tenant/user transaction, and takes location scope only from the
application. Model arguments cannot redirect it to another site.

The first read tool, `search_catalog`, is owned by `catalog` and calls the same
`Store.Quotable` contract reserved for future telephone agents. It returns only
currently effective prices applicable to the scoped accessible location,
preserving fixed/from/range/quote, HT/TTC, duration, and reference semantics.
Read executions run immediately, are still audited, and have no fake
confirmation timestamp.

`search_customers` reuses the customer search contract for names, companies,
phones, emails, plates, and VINs, then applies the conversation location again:
only a customer owned by that site or actively shared to it is returned. This
extra filter matters for owners who can access several sites at once; their
conversation scope is narrower than their membership scope.

## Confirmation, concurrency, and audit

A write call first produces canonical validated arguments and a human-readable
before/after preview. PostgreSQL persists a `proposed` execution and immutable
affected-record list. No domain row changes until the employee presses
**Confirmer l’action**.

The canonical proposal carries the location's observed `updated_at`. Execution
uses that version in its `UPDATE`, so a concurrent human edit turns the action
into a recoverable conflict instead of being overwritten. The employee must ask
for a fresh preview.

The domain mutation and a generic `application_tool_receipts` row commit in the
same transaction. Its tenant-scoped idempotency key means a process crash after
the mutation but before the conversation audit is finalized can resume without
replaying the change. Messages, terminal tool audits, and application receipts
are immutable even to direct SQL writes enforced by triggers.

Each audit records the requesting user, conversation, location, tool,
consequence class, idempotency key, validated input, preview, affected records,
status, safe output/error, and proposal/confirmation/completion times. Forced
RLS independently enforces ownership and location access.

## Current limitation

This tracer bullet intentionally exposes one reversible write tool, catalog and
customer/vehicle read tools, and a fake model adapter. Customer corrections and
appointment tools; real model providers; richer model follow-up turns; and stricter
destructive-action policies remain separate slices.
