ALTER TABLE rentals
    DROP CONSTRAINT rentals_lifecycle_timestamps_check,
    ADD COLUMN expected_return_at timestamptz;

UPDATE rentals
SET expected_return_at = planned_end_at
WHERE status IN ('active', 'completed');

ALTER TABLE rentals
    ADD CONSTRAINT rentals_lifecycle_timestamps_check CHECK (
        (
            status IN ('confirmed', 'cancelled')
            AND issued_at IS NULL
            AND expected_return_at IS NULL
            AND returned_at IS NULL
        )
        OR
        (
            status = 'active'
            AND issued_at IS NOT NULL
            AND expected_return_at IS NOT NULL
            AND returned_at IS NULL
        )
        OR
        (
            status = 'completed'
            AND issued_at IS NOT NULL
            AND expected_return_at IS NOT NULL
            AND returned_at IS NOT NULL
            AND returned_at >= issued_at
        )
    );

---- create above / drop below ----

ALTER TABLE rentals
    DROP CONSTRAINT rentals_lifecycle_timestamps_check,
    DROP COLUMN expected_return_at,
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
