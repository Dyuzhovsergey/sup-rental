ALTER TABLE rentals
    ADD COLUMN overdue_slots integer,
    ADD COLUMN final_total_kopecks bigint;

WITH completed_settlements AS (
    SELECT
        r.id,
        CASE
            WHEN r.returned_at <= r.planned_end_at + interval '10 minutes' THEN 0
            ELSE ceil(
                extract(epoch FROM (r.returned_at - r.planned_end_at)) / 1800
            )::integer
        END AS overdue_slots,
        (extract(epoch FROM (r.planned_end_at - r.planned_start_at)) / 1800)::bigint
            AS planned_slots,
        sum(ri.hourly_rate_kopecks)::numeric / 2 AS half_hourly_total
    FROM rentals AS r
    JOIN rental_items AS ri ON ri.rental_id = r.id
    WHERE r.status = 'completed'
    GROUP BY r.id
)
UPDATE rentals AS r
SET overdue_slots = s.overdue_slots,
    final_total_kopecks = (
        s.half_hourly_total * (s.planned_slots + s.overdue_slots)
    )::bigint
FROM completed_settlements AS s
WHERE r.id = s.id;

ALTER TABLE rentals
    ADD CONSTRAINT rentals_settlement_status_check CHECK (
        (
            status = 'completed'
            AND overdue_slots IS NOT NULL
            AND overdue_slots >= 0
            AND final_total_kopecks IS NOT NULL
            AND final_total_kopecks > 0
        )
        OR
        (
            status <> 'completed'
            AND overdue_slots IS NULL
            AND final_total_kopecks IS NULL
        )
    );

---- create above / drop below ----

ALTER TABLE rentals
    DROP CONSTRAINT rentals_settlement_status_check,
    DROP COLUMN final_total_kopecks,
    DROP COLUMN overdue_slots;
