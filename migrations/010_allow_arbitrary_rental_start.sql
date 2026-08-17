ALTER TABLE rentals
    DROP CONSTRAINT rentals_start_slot_check,
    DROP CONSTRAINT rentals_end_slot_check,
    ADD CONSTRAINT rentals_start_minute_check CHECK (
        date_trunc('minute', planned_start_at) = planned_start_at
    ),
    ADD CONSTRAINT rentals_end_minute_check CHECK (
        date_trunc('minute', planned_end_at) = planned_end_at
    ),
    ADD CONSTRAINT rentals_duration_slot_check CHECK (
        mod(
            extract(epoch FROM planned_end_at - planned_start_at),
            1800
        ) = 0
    ),
    ADD CONSTRAINT rentals_duration_max_check CHECK (
        planned_end_at <= planned_start_at + interval '12 hours'
    );

---- create above / drop below ----

ALTER TABLE rentals
    DROP CONSTRAINT rentals_start_minute_check,
    DROP CONSTRAINT rentals_end_minute_check,
    DROP CONSTRAINT rentals_duration_slot_check,
    DROP CONSTRAINT rentals_duration_max_check,
    ADD CONSTRAINT rentals_start_slot_check CHECK (
        date_bin(
            interval '30 minutes',
            planned_start_at,
            timestamptz '1970-01-01 00:00:00+00'
        ) = planned_start_at
    ),
    ADD CONSTRAINT rentals_end_slot_check CHECK (
        date_bin(
            interval '30 minutes',
            planned_end_at,
            timestamptz '1970-01-01 00:00:00+00'
        ) = planned_end_at
    );
