ALTER TABLE rentals
    ADD COLUMN issued_at timestamptz,
    ADD CONSTRAINT rentals_issued_at_status_check CHECK (
        (status IN ('confirmed', 'cancelled') AND issued_at IS NULL)
        OR
        (status IN ('active', 'completed') AND issued_at IS NOT NULL)
    );

---- create above / drop below ----

ALTER TABLE rentals
    DROP CONSTRAINT rentals_issued_at_status_check,
    DROP COLUMN issued_at;
