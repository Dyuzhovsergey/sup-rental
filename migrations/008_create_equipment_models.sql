CREATE TABLE equipment_models (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind text NOT NULL,
    model_code text NOT NULL,
    hourly_rate_kopecks bigint NOT NULL,
    next_sequence bigint NOT NULL DEFAULT 1,

    CONSTRAINT equipment_models_kind_check CHECK (
        kind IN ('sup_board', 'paddle', 'life_jacket')
    ),
    CONSTRAINT equipment_models_code_check CHECK (
        model_code ~ '^[A-Z0-9]+(-[A-Z0-9]+)*$'
    ),
    CONSTRAINT equipment_models_rate_check CHECK (
        hourly_rate_kopecks > 0 AND hourly_rate_kopecks % 100 = 0
    ),
    CONSTRAINT equipment_models_next_sequence_check CHECK (next_sequence > 0),
    CONSTRAINT equipment_models_kind_code_key UNIQUE (kind, model_code)
);

DELETE FROM equipment;

DROP INDEX equipment_inventory_number_lower_idx;

ALTER TABLE equipment
    DROP COLUMN inventory_number,
    DROP COLUMN kind,
    ADD COLUMN model_id bigint NOT NULL REFERENCES equipment_models (id),
    ADD COLUMN sequence_number bigint NOT NULL,
    ADD CONSTRAINT equipment_sequence_number_check CHECK (sequence_number > 0),
    ADD CONSTRAINT equipment_model_sequence_key UNIQUE (model_id, sequence_number);

---- create above / drop below ----

ALTER TABLE equipment
    ADD COLUMN inventory_number text,
    ADD COLUMN kind text;

UPDATE equipment AS e
SET inventory_number = CASE m.kind
        WHEN 'sup_board' THEN 'SUP'
        WHEN 'paddle' THEN 'PADDLE'
        WHEN 'life_jacket' THEN 'VEST'
    END || '-' || m.model_code || '-' || e.sequence_number,
    kind = m.kind
FROM equipment_models AS m
WHERE m.id = e.model_id;

ALTER TABLE equipment
    ALTER COLUMN inventory_number SET NOT NULL,
    ALTER COLUMN kind SET NOT NULL,
    ADD CONSTRAINT equipment_kind_check CHECK (
        kind IN ('sup_board', 'paddle', 'life_jacket')
    ),
    DROP CONSTRAINT equipment_model_sequence_key,
    DROP CONSTRAINT equipment_sequence_number_check,
    DROP COLUMN sequence_number,
    DROP COLUMN model_id;

CREATE UNIQUE INDEX equipment_inventory_number_lower_idx
    ON equipment (lower(inventory_number));

DROP TABLE equipment_models;
