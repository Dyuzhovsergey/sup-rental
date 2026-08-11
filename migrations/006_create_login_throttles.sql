CREATE TABLE login_throttles (
    key_type text NOT NULL,
    key_digest bytea NOT NULL,
    window_started_at timestamptz NOT NULL,
    failure_count integer NOT NULL,
    blocked_until timestamptz,

    CONSTRAINT login_throttles_key_type_check CHECK (
        key_type IN ('login', 'ip')
    ),
    CONSTRAINT login_throttles_key_digest_length_check CHECK (
        octet_length(key_digest) = 32
    ),
    CONSTRAINT login_throttles_failure_count_check CHECK (
        failure_count > 0
    ),
    CONSTRAINT login_throttles_primary_key PRIMARY KEY (key_type, key_digest)
);

CREATE INDEX login_throttles_blocked_until_idx
    ON login_throttles (blocked_until)
    WHERE blocked_until IS NOT NULL;

---- create above / drop below ----

DROP TABLE login_throttles;
