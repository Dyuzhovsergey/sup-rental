DELETE FROM rentals
WHERE status = 'draft';

ALTER TABLE rentals
    ALTER COLUMN status SET DEFAULT 'confirmed',
    DROP CONSTRAINT rentals_status_check,
    ADD CONSTRAINT rentals_status_check CHECK (
        status IN ('confirmed', 'active', 'completed', 'cancelled')
    );

---- create above / drop below ----

ALTER TABLE rentals
    ALTER COLUMN status SET DEFAULT 'draft',
    DROP CONSTRAINT rentals_status_check,
    ADD CONSTRAINT rentals_status_check CHECK (
        status IN ('draft', 'confirmed', 'active', 'completed', 'cancelled')
    );
