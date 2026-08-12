CREATE TABLE clients (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    full_name text NOT NULL,
    phone text NOT NULL,

    CONSTRAINT clients_full_name_length_check CHECK (
        char_length(full_name) BETWEEN 1 AND 200
    ),
    CONSTRAINT clients_full_name_trimmed_check CHECK (
        full_name = btrim(full_name)
    ),
    CONSTRAINT clients_phone_format_check CHECK (
        phone ~ '^\+[1-9][0-9]{7,14}$'
    ),
    CONSTRAINT clients_phone_unique UNIQUE (phone)
);

---- create above / drop below ----

DROP TABLE clients;
