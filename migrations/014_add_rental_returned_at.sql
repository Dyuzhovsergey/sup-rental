DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM rentals WHERE status = 'completed') THEN
        RAISE EXCEPTION 'migration 014 cannot infer returned_at for existing completed rentals'
            USING HINT = 'Verify legacy completed rentals and record an explicit return time before applying this migration.';
    END IF;
END
$$;

ALTER TABLE rentals
    DROP CONSTRAINT rentals_issued_at_status_check,
    ADD COLUMN returned_at timestamptz,
    ADD CONSTRAINT rentals_lifecycle_timestamps_check CHECK (
        (
            status IN ('confirmed', 'cancelled')
            AND issued_at IS NULL
            AND returned_at IS NULL
        )
        OR
        (
            status = 'active'
            AND issued_at IS NOT NULL
            AND returned_at IS NULL
        )
        OR
        (
            status = 'completed'
            AND issued_at IS NOT NULL
            AND returned_at IS NOT NULL
            AND returned_at >= issued_at
        )
    );

---- create above / drop below ----

ALTER TABLE rentals
    DROP CONSTRAINT rentals_lifecycle_timestamps_check,
    DROP COLUMN returned_at,
    ADD CONSTRAINT rentals_issued_at_status_check CHECK (
        (status IN ('confirmed', 'cancelled') AND issued_at IS NULL)
        OR
        (status IN ('active', 'completed') AND issued_at IS NOT NULL)
    );
