-- A garage is three people, and two of them have no reason to own an email
-- account or a Google login: the owner enrols them, hands over a link, and they
-- are in. This table holds that link's single-use token.
--
-- It carries no row security, on purpose and like sessions: the visitor
-- following an invitation is anonymous, so there is no tenant or user context
-- for a policy to read. The token is the capability — 260 bits of entropy,
-- stored only as a SHA-256 hash so a leaked table cannot be replayed — and
-- creating one is already gated, because the membership it points at can only
-- be written by an owner or an admin (migration 0029).

-- +goose Up
CREATE TABLE staff_invites (
    id           UUID PRIMARY KEY DEFAULT uuidv7(),
    tenant_id    UUID NOT NULL,
    user_id      UUID NOT NULL,
    token_hash   TEXT NOT NULL,
    created_by_user_id UUID NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    expires_at   TIMESTAMPTZ NOT NULL,
    accepted_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- Removing someone from the organization must take their pending link with
    -- them, so the invitation hangs off the membership rather than the user.
    FOREIGN KEY (tenant_id, user_id)
        REFERENCES tenant_memberships (tenant_id, user_id) ON DELETE CASCADE,
    UNIQUE (token_hash),
    CONSTRAINT staff_invites_token_hash_present CHECK (btrim(token_hash) <> ''),
    CONSTRAINT staff_invites_accepted_after_creation CHECK (
        accepted_at IS NULL OR accepted_at >= created_at
    )
);

-- One live invitation per person: re-inviting replaces the previous link
-- instead of leaving several valid ones in circulation.
CREATE UNIQUE INDEX staff_invites_pending_unique
    ON staff_invites (tenant_id, user_id)
    WHERE accepted_at IS NULL;

-- +goose Down
DROP TABLE staff_invites;
