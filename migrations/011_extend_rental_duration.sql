ALTER TABLE rentals
    DROP CONSTRAINT rentals_duration_max_check,
    ADD CONSTRAINT rentals_duration_max_check CHECK (
        planned_end_at <= planned_start_at
            + interval '31 days 23 hours 30 minutes'
    );

---- create above / drop below ----

ALTER TABLE rentals
    DROP CONSTRAINT rentals_duration_max_check,
    ADD CONSTRAINT rentals_duration_max_check CHECK (
        planned_end_at <= planned_start_at + interval '12 hours'
    );
