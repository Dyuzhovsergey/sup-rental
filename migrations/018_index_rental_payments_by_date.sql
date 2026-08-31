CREATE INDEX rental_payments_occurred_at_id_idx
    ON rental_payments (occurred_at DESC, id DESC);

---- create above / drop below ----

DROP INDEX rental_payments_occurred_at_id_idx;
