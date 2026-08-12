package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const uniqueEquipmentInventoryNumberConstraint = "equipment_inventory_number_lower_idx"
const foreignKeyViolationCode = "23503"

const (
	actionEquipmentCreated       = "equipment.created"
	actionEquipmentUpdated       = "equipment.updated"
	actionEquipmentStatusChanged = "equipment.status_changed"
	actionEquipmentRetired       = "equipment.retired"
	actionEquipmentDeleted       = "equipment.deleted"
)

type equipmentAuditWriter func(
	ctx context.Context,
	tx pgx.Tx,
	action string,
	actor user.User,
	target equipment.Item,
	details equipmentAuditDetails,
) error

// EquipmentRepository хранит оборудование в PostgreSQL.
type EquipmentRepository struct {
	pool       *pgxpool.Pool
	writeAudit equipmentAuditWriter
}

// NewEquipmentRepository создаёт PostgreSQL repository оборудования.
func NewEquipmentRepository(pool *pgxpool.Pool) *EquipmentRepository {
	return &EquipmentRepository{pool: pool, writeAudit: writeEquipmentAudit}
}

// Create сохраняет оборудование и возвращает данные с назначенным ID.
func (r *EquipmentRepository) Create(
	ctx context.Context,
	actor user.User,
	item equipment.Item,
) (equipment.Item, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return equipment.Item{}, fmt.Errorf("begin create equipment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const query = `
		INSERT INTO equipment (inventory_number, kind, status)
		VALUES ($1, $2, $3)
		RETURNING id, inventory_number, kind, status
	`

	var created equipment.Item
	err = tx.QueryRow(
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

	if err := r.writeAudit(
		ctx,
		tx,
		actionEquipmentCreated,
		actor,
		created,
		equipmentAuditDetails{After: equipmentAuditSnapshotFor(created)},
	); err != nil {
		return equipment.Item{}, fmt.Errorf("write create equipment audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return equipment.Item{}, fmt.Errorf("commit create equipment transaction: %w", err)
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

// Update сохраняет инвентарный номер, тип и состояние одной SQL-командой.
func (r *EquipmentRepository) Update(
	ctx context.Context,
	actor user.User,
	id int64,
	inventoryNumber string,
	kind equipment.Kind,
	status equipment.Status,
) (equipment.Item, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return equipment.Item{}, fmt.Errorf("begin update equipment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	before, err := lockEquipment(ctx, tx, id)
	if err != nil {
		return equipment.Item{}, err
	}

	const query = `
		UPDATE equipment
		SET inventory_number = $1, kind = $2, status = $3
		WHERE id = $4
		RETURNING id, inventory_number, kind, status
	`

	item, err := queryEquipment(ctx, tx, query, inventoryNumber, kind, status, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return equipment.Item{}, equipment.ErrEquipmentNotFound
	}
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) &&
			postgresError.ConstraintName == uniqueEquipmentInventoryNumberConstraint {
			return equipment.Item{}, equipment.ErrInventoryNumberExists
		}

		return equipment.Item{}, fmt.Errorf("update equipment: %w", err)
	}

	action := actionEquipmentUpdated
	if before.InventoryNumber == item.InventoryNumber &&
		before.Kind == item.Kind && before.Status != item.Status {
		action = actionEquipmentStatusChanged
	}
	if err := r.writeAudit(
		ctx,
		tx,
		action,
		actor,
		item,
		equipmentAuditDetails{
			Before: equipmentAuditSnapshotFor(before),
			After:  equipmentAuditSnapshotFor(item),
		},
	); err != nil {
		return equipment.Item{}, fmt.Errorf("write update equipment audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return equipment.Item{}, fmt.Errorf("commit update equipment transaction: %w", err)
	}

	return item, nil
}

// UpdateStatus сохраняет новое физическое состояние оборудования по ID.
func (r *EquipmentRepository) UpdateStatus(
	ctx context.Context,
	actor user.User,
	id int64,
	status equipment.Status,
) (equipment.Item, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return equipment.Item{}, fmt.Errorf("begin update equipment status transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	before, err := lockEquipment(ctx, tx, id)
	if err != nil {
		return equipment.Item{}, err
	}

	const query = `
		UPDATE equipment
		SET status = $1
		WHERE id = $2
		RETURNING id, inventory_number, kind, status
	`

	item, err := queryEquipment(ctx, tx, query, status, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return equipment.Item{}, equipment.ErrEquipmentNotFound
	}
	if err != nil {
		return equipment.Item{}, fmt.Errorf("update equipment status: %w", err)
	}

	action := actionEquipmentStatusChanged
	if status == equipment.StatusRetired {
		action = actionEquipmentRetired
	}
	if err := r.writeAudit(
		ctx,
		tx,
		action,
		actor,
		item,
		equipmentAuditDetails{
			Before: equipmentAuditSnapshotFor(before),
			After:  equipmentAuditSnapshotFor(item),
		},
	); err != nil {
		return equipment.Item{}, fmt.Errorf("write equipment status audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return equipment.Item{}, fmt.Errorf("commit update equipment status transaction: %w", err)
	}

	return item, nil
}

// Delete безвозвратно удаляет оборудование по ID.
//
// Если строка связана внешним ключом с историческими данными, метод возвращает
// equipment.ErrEquipmentHasHistory.
func (r *EquipmentRepository) Delete(
	ctx context.Context,
	actor user.User,
	id int64,
) (equipment.Item, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return equipment.Item{}, fmt.Errorf("begin delete equipment transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	item, err := lockEquipment(ctx, tx, id)
	if err != nil {
		return equipment.Item{}, err
	}

	const query = `
		DELETE FROM equipment
		WHERE id = $1
	`

	commandTag, err := tx.Exec(ctx, query, id)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) &&
			postgresError.Code == foreignKeyViolationCode {
			return equipment.Item{}, equipment.ErrEquipmentHasHistory
		}

		return equipment.Item{}, fmt.Errorf("delete equipment: %w", err)
	}

	if commandTag.RowsAffected() == 0 {
		return equipment.Item{}, equipment.ErrEquipmentNotFound
	}
	if err := r.writeAudit(
		ctx,
		tx,
		actionEquipmentDeleted,
		actor,
		item,
		equipmentAuditDetails{Before: equipmentAuditSnapshotFor(item)},
	); err != nil {
		return equipment.Item{}, fmt.Errorf("write delete equipment audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return equipment.Item{}, fmt.Errorf("commit delete equipment transaction: %w", err)
	}

	return item, nil
}

func (r *EquipmentRepository) queryEquipment(
	ctx context.Context,
	query string,
	arguments ...any,
) (equipment.Item, error) {
	return queryEquipment(ctx, r.pool, query, arguments...)
}

type equipmentQueryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func queryEquipment(
	ctx context.Context,
	database equipmentQueryer,
	query string,
	arguments ...any,
) (equipment.Item, error) {
	var item equipment.Item
	err := database.QueryRow(ctx, query, arguments...).Scan(
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

func lockEquipment(ctx context.Context, tx pgx.Tx, id int64) (equipment.Item, error) {
	const query = `
		SELECT id, inventory_number, kind, status
		FROM equipment
		WHERE id = $1
		FOR UPDATE
	`
	item, err := queryEquipment(ctx, tx, query, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return equipment.Item{}, equipment.ErrEquipmentNotFound
	}
	if err != nil {
		return equipment.Item{}, fmt.Errorf("lock equipment: %w", err)
	}
	return item, nil
}

type equipmentAuditSnapshot struct {
	InventoryNumber string           `json:"inventory_number"`
	Kind            equipment.Kind   `json:"kind"`
	Status          equipment.Status `json:"status"`
}

type equipmentAuditDetails struct {
	Before *equipmentAuditSnapshot `json:"before,omitempty"`
	After  *equipmentAuditSnapshot `json:"after,omitempty"`
}

func equipmentAuditSnapshotFor(item equipment.Item) *equipmentAuditSnapshot {
	return &equipmentAuditSnapshot{
		InventoryNumber: item.InventoryNumber,
		Kind:            item.Kind,
		Status:          item.Status,
	}
}

func writeEquipmentAudit(
	ctx context.Context,
	tx pgx.Tx,
	action string,
	actor user.User,
	target equipment.Item,
	details equipmentAuditDetails,
) error {
	encodedDetails, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode equipment audit details: %w", err)
	}

	const query = `
		INSERT INTO audit_events (
			actor_user_id,
			actor_login,
			actor_role,
			action,
			target_type,
			target_id,
			target_label,
			result,
			details
		)
		VALUES ($1, $2, $3, $4, 'equipment', $5, $6, 'success', $7::jsonb)
	`
	if _, err := tx.Exec(
		ctx,
		query,
		actor.ID,
		actor.Login,
		actor.Role,
		action,
		target.ID,
		target.InventoryNumber,
		encodedDetails,
	); err != nil {
		return fmt.Errorf("insert equipment audit event: %w", err)
	}
	return nil
}
