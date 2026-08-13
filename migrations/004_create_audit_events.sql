CREATE TABLE audit_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    actor_user_id bigint REFERENCES users (id),
    actor_login text,
    actor_role text,
    action text NOT NULL CHECK (action <> ''),
    target_type text NOT NULL CHECK (target_type <> ''),
    target_id bigint,
    target_label text NOT NULL CHECK (target_label <> ''),
    result text NOT NULL CHECK (result IN ('success', 'failure')),
    details jsonb NOT NULL DEFAULT '{}'::jsonb
        CHECK (jsonb_typeof(details) = 'object'),

    CONSTRAINT audit_events_actor_role_check CHECK (
        actor_role IS NULL OR actor_role IN ('admin', 'operator')
    )
);

CREATE INDEX audit_events_occurred_at_idx
    ON audit_events (occurred_at DESC);

---- create above / drop below ----

DROP TABLE audit_events;
