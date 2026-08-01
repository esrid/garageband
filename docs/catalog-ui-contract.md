# Catalog UI and HTTP contract

The `catalog` feature is the organization-owned source of truth for prices that
an AI agent may quote. `service_offerings` remains the scheduling contract until
the service-administration slice deliberately links the two concepts.

## Routes

All routes require an authenticated user and an active organization.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/catalog` | List and search visible published items; optional `kind` and `q` query parameters. |
| `GET` | `/catalog/new` | Show the manual item form. |
| `POST` | `/catalog` | Validate and create a published item. |
| `GET` | `/catalog/{itemID}` | Show an item in editable or read-only form according to role. |
| `POST` | `/catalog/{itemID}` | Validate and update an item. |
| `POST` | `/catalog/{itemID}/delete` | Soft-archive a manually managed item. |
| `GET` | `/catalog/imports` | List visible import batches. |
| `GET` | `/catalog/imports/new` | Show the upload form. |
| `POST` | `/catalog/imports` | Accept and synchronously classify one CSV or XLSX file. |
| `GET` | `/catalog/imports/{importID}` | Preview rows and, with `mode=merge|replace`, the exact publication plan. |
| `POST` | `/catalog/imports/{importID}/publish` | Publish once after `confirm=1`. |
| `POST` | `/catalog/imports/{importID}/cancel` | Abandon a ready import without changing the catalog. |

Owners and administrators may mutate the catalog. Managers and members see
only organization-wide items plus items assigned to locations they can access.
PostgreSQL RLS enforces both rules independently from the handlers.

## Manual item form

Prices are integer cents throughout Go and PostgreSQL; browser decimal strings
never pass through a floating-point value. Supported price forms are fixed,
from, range, and quote-only. Tax basis (HT/TTC), VAT basis points, effective
dates, and all-sites versus selected-sites scope are explicit.

The handler returns `422` with the submitted strings preserved for malformed
fields, `409` for a duplicate active reference, `403` for a forbidden write,
and `404` for an inaccessible or unknown record. Deletion is archival, not a
runtime SQL `DELETE`.

## Imports

Uploads are limited to 5 MiB and retained in PostgreSQL with filename, media
type, observed size, SHA-256 checksum, uploader, source bytes, and timestamps.
The same checksum cannot create another batch for the same organization and
location. Rejected file attempts retain audit metadata; oversized content is
not retained.

CSV delimiter detection covers semicolon, comma, and tab. CSV must be UTF-8.
XLSX reads the first worksheet with bounded ZIP-part decompression. Common
French and English column names are recognized for:

- name/label;
- kind;
- reference/code;
- description;
- amount and maximum amount;
- fixed/from/range/quote price kind;
- HT/TTC basis and VAT rate;
- duration and effective dates.

The minimum is a recognizable name column and either an amount or price-kind
column. Dates use ISO `YYYY-MM-DD` or French `DD/MM/YYYY`; currency is EUR.

Every non-empty source row becomes one immutable staging row:

- `valid`: creates an item;
- `ambiguous`: updates one existing item confined to the import location;
- `rejected`: never publishes.

A collision with an all-site or multi-site item is rejected rather than
silently changing another location's price. Duplicate rows inside one file are
also rejected.

Merge creates valid rows and updates safe ambiguous rows. Replace additionally
archives single-location items at the import location that are absent from the
file; it never removes all-site or multi-site items. The preview count and the
transaction use the same predicates.

Publication locks the batch, is single-use, records a monotonically increasing
organization version, and stores before/after snapshots. `Store.Rollback`
restores only the latest unchanged publication; later publications or manual
edits produce `ErrPublicationChanged` instead of overwriting newer work.

## Agent read contract

`Store.Quotable` requires an accessible active location and returns at most 20
currently effective, non-archived matching items. It never returns an expired,
future, wrong-location, or inaccessible price. Future telephone and internal
assistant tools must call this contract rather than querying catalog tables or
inventing a fallback amount.
