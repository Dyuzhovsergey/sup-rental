CREATE TABLE equipment (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    inventory_number text NOT NULL
        CHECK (
            inventory_number = btrim(inventory_number)
            AND inventory_number <> ''
        ),
    kind text NOT NULL
        CHECK (kind IN ('sup_board', 'paddle', 'life_jacket')),
    status text NOT NULL DEFAULT 'available'
        CHECK (status IN ('available', 'issued', 'maintenance', 'retired'))
);

CREATE UNIQUE INDEX equipment_inventory_number_lower_idx
    ON equipment (lower(inventory_number));

---- create above / drop below ----

DROP TABLE equipment;
