CREATE TABLE sessions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id bigint NOT NULL REFERENCES users (id),
    token_digest bytea NOT NULL,
    csrf_token text NOT NULL,
    created_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    absolute_expires_at timestamptz NOT NULL,
    revoked_at timestamptz,

    CONSTRAINT sessions_token_digest_length_check CHECK (
        octet_length(token_digest) = 32
    ),
    CONSTRAINT sessions_csrf_token_not_empty_check CHECK (
        csrf_token <> ''
    ),
    CONSTRAINT sessions_time_order_check CHECK (
        last_seen_at >= created_at
        AND absolute_expires_at > created_at
        AND (revoked_at IS NULL OR revoked_at >= created_at)
    ),
    CONSTRAINT sessions_token_digest_unique UNIQUE (token_digest)
);

CREATE INDEX sessions_active_user_idx
    ON sessions (user_id)
    WHERE revoked_at IS NULL;

---- create above / drop below ----

DROP TABLE sessions;
