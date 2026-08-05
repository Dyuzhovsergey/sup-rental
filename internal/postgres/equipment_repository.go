package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const uniqueEquipmentInventoryNumberConstraint = "equipment_inventory_number_lower_idx"

// EquipmentRepository хранит оборудование в PostgreSQL.
type EquipmentRepository struct {
	pool *pgxpool.Pool
}

// NewEquipmentRepository создаёт PostgreSQL repository оборудования.
func NewEquipmentRepository(pool *pgxpool.Pool) *EquipmentRepository {
	return &EquipmentRepository{pool: pool}
}

// Create сохраняет оборудование и возвращает данные с назначенным ID.
func (r *EquipmentRepository) Create(
	ctx context.Context,
	item equipment.Item,
) (equipment.Item, error) {
	const query = `
		INSERT INTO equipment (inventory_number, kind, status)
		VALUES ($1, $2, $3)
		RETURNING id, inventory_number, kind, status
	`

	var created equipment.Item
	err := r.pool.QueryRow(
		ctx,
		query,
		item.InventoryNumber,
		item.Kind,
		item.Status,
	).Scan(
		&created.ID,
		&created.InventoryNumber,
		&created.Kind,
		&created.Status,
	)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) &&
			postgresError.ConstraintName == uniqueEquipmentInventoryNumberConstraint {
			return equipment.Item{}, equipment.ErrInventoryNumberExists
		}

		return equipment.Item{}, fmt.Errorf("insert equipment: %w", err)
	}

	return created, nil
}

// List возвращает всё оборудование в порядке возрастания ID.
func (r *EquipmentRepository) List(ctx context.Context) ([]equipment.Item, error) {
	const query = `
		SELECT id, inventory_number, kind, status
		FROM equipment
		ORDER BY id
	`

	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query equipment: %w", err)
	}
	defer rows.Close()

	items := make([]equipment.Item, 0)
	for rows.Next() {
		var item equipment.Item
		if err := rows.Scan(
			&item.ID,
			&item.InventoryNumber,
			&item.Kind,
			&item.Status,
		); err != nil {
			return nil, fmt.Errorf("scan equipment: %w", err)
		}

		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate equipment: %w", err)
	}

	return items, nil
}

// Get возвращает оборудование по ID.
func (r *EquipmentRepository) Get(ctx context.Context, id int64) (equipment.Item, error) {
	const query = `
		SELECT id, inventory_number, kind, status
		FROM equipment
		WHERE id = $1
	`

	item, err := r.queryEquipment(ctx, query, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return equipment.Item{}, equipment.ErrEquipmentNotFound
	}
	if err != nil {
		return equipment.Item{}, fmt.Errorf("get equipment: %w", err)
	}

	return item, nil
}

// UpdateDetails сохраняет новый инвентарный номер и тип оборудования по ID.
func (r *EquipmentRepository) UpdateDetails(
	ctx context.Context,
	id int64,
	inventoryNumber string,
	kind equipment.Kind,
) (equipment.Item, error) {
	const query = `
		UPDATE equipment
		SET inventory_number = $1, kind = $2
		WHERE id = $3
		RETURNING id, inventory_number, kind, status
	`

	item, err := r.queryEquipment(ctx, query, inventoryNumber, kind, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return equipment.Item{}, equipment.ErrEquipmentNotFound
	}
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) &&
			postgresError.ConstraintName == uniqueEquipmentInventoryNumberConstraint {
			return equipment.Item{}, equipment.ErrInventoryNumberExists
		}

		return equipment.Item{}, fmt.Errorf("update equipment details: %w", err)
	}

	return item, nil
}

// UpdateStatus сохраняет новое физическое состояние оборудования по ID.
func (r *EquipmentRepository) UpdateStatus(
	ctx context.Context,
	id int64,
	status equipment.Status,
) (equipment.Item, error) {
	const query = `
		UPDATE equipment
		SET status = $1
		WHERE id = $2
		RETURNING id, inventory_number, kind, status
	`

	item, err := r.queryEquipment(ctx, query, status, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return equipment.Item{}, equipment.ErrEquipmentNotFound
	}
	if err != nil {
		return equipment.Item{}, fmt.Errorf("update equipment status: %w", err)
	}

	return item, nil
}

func (r *EquipmentRepository) queryEquipment(
	ctx context.Context,
	query string,
	arguments ...any,
) (equipment.Item, error) {
	var item equipment.Item
	err := r.pool.QueryRow(ctx, query, arguments...).Scan(
		&item.ID,
		&item.InventoryNumber,
		&item.Kind,
		&item.Status,
	)
	if err != nil {
		return equipment.Item{}, err
	}

	return item, nil
}
