CREATE TABLE rentals (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    client_id bigint NOT NULL,
    planned_start_at timestamptz NOT NULL,
    planned_end_at timestamptz NOT NULL,
    status text NOT NULL DEFAULT 'draft',

    CONSTRAINT rentals_client_id_fkey FOREIGN KEY (client_id)
        REFERENCES clients (id),
    CONSTRAINT rentals_status_check CHECK (
        status IN ('draft', 'confirmed', 'active', 'completed', 'cancelled')
    ),
    CONSTRAINT rentals_time_order_check CHECK (
        planned_end_at >= planned_start_at + interval '30 minutes'
    ),
    CONSTRAINT rentals_start_slot_check CHECK (
        date_bin(
            interval '30 minutes',
            planned_start_at,
            timestamptz '1970-01-01 00:00:00+00'
        ) = planned_start_at
    ),
    CONSTRAINT rentals_end_slot_check CHECK (
        date_bin(
            interval '30 minutes',
            planned_end_at,
            timestamptz '1970-01-01 00:00:00+00'
        ) = planned_end_at
    )
);

CREATE INDEX rentals_client_id_idx
    ON rentals (client_id, id DESC);

CREATE TABLE rental_items (
    rental_id bigint NOT NULL,
    equipment_id bigint NOT NULL,
    position integer NOT NULL,
    inventory_number text NOT NULL,
    kind text NOT NULL,
    model_code text NOT NULL,
    hourly_rate_kopecks bigint NOT NULL,

    CONSTRAINT rental_items_rental_id_fkey FOREIGN KEY (rental_id)
        REFERENCES rentals (id) ON DELETE CASCADE,
    CONSTRAINT rental_items_equipment_id_fkey FOREIGN KEY (equipment_id)
        REFERENCES equipment (id),
    CONSTRAINT rental_items_position_check CHECK (position > 0),
    CONSTRAINT rental_items_kind_check CHECK (
        kind IN ('sup_board', 'paddle', 'life_jacket')
    ),
    CONSTRAINT rental_items_model_code_check CHECK (
        model_code ~ '^[A-Z0-9]+(-[A-Z0-9]+)*$'
    ),
    CONSTRAINT rental_items_inventory_number_check CHECK (
        inventory_number ~ (
            '^' || CASE kind
                WHEN 'sup_board' THEN 'SUP'
                WHEN 'paddle' THEN 'PADDLE'
                WHEN 'life_jacket' THEN 'VEST'
            END || '-' || model_code || '-[1-9][0-9]*$'
        )
    ),
    CONSTRAINT rental_items_hourly_rate_check CHECK (
        hourly_rate_kopecks > 0 AND hourly_rate_kopecks % 2 = 0
    ),
    CONSTRAINT rental_items_pkey PRIMARY KEY (rental_id, position),
    CONSTRAINT rental_items_rental_equipment_key UNIQUE (rental_id, equipment_id)
);

CREATE INDEX rental_items_equipment_id_idx
    ON rental_items (equipment_id);

---- create above / drop below ----

DROP TABLE rental_items;
DROP TABLE rentals;
