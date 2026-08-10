CREATE TABLE users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    login text NOT NULL,
    password_hash text NOT NULL,
    role text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    last_login_at timestamptz,

    CONSTRAINT users_login_format_check CHECK (
        login ~ '^[a-z0-9][a-z0-9._-]{2,31}$'
    ),
    CONSTRAINT users_password_hash_not_empty_check CHECK (
        password_hash <> ''
    ),
    CONSTRAINT users_role_check CHECK (
        role IN ('admin', 'operator')
    ),
    CONSTRAINT users_admin_active_check CHECK (
        role <> 'admin' OR active
    ),
    CONSTRAINT users_login_unique UNIQUE (login)
);

CREATE UNIQUE INDEX users_single_admin_idx
    ON users (role)
    WHERE role = 'admin';

---- create above / drop below ----

DROP TABLE users;
