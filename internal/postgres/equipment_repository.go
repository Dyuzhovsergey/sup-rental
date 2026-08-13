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

const foreignKeyViolationCode = "23503"

const (
	actionEquipmentBatchCreated  = "equipment.batch_created"
	actionEquipmentModelChanged  = "equipment.model_changed"
	actionEquipmentRateChanged   = "equipment.model_rate_changed"
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

// EquipmentRepository хранит модели и физические единицы оборудования в PostgreSQL.
type EquipmentRepository struct {
	pool       *pgxpool.Pool
	writeAudit equipmentAuditWriter
}

// NewEquipmentRepository создаёт PostgreSQL repository оборудования.
func NewEquipmentRepository(pool *pgxpool.Pool) *EquipmentRepository {
	return &EquipmentRepository{pool: pool, writeAudit: writeEquipmentAudit}
}

// CreateBatch атомарно выделяет диапазон номеров, создаёт физические единицы и
// записывает одно audit event для всей партии.
func (r *EquipmentRepository) CreateBatch(
	ctx context.Context,
	actor user.User,
	input equipment.BatchCreateInput,
) (equipment.Batch, error) {
	hourlyRateKopecks, err := equipment.HourlyRateKopecks(input.HourlyRateRubles)
	if err != nil {
		return equipment.Batch{}, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return equipment.Batch{}, fmt.Errorf("begin create equipment batch transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	model, err := lockOrCreateEquipmentModel(
		ctx, tx, input.Kind, input.ModelCode, hourlyRateKopecks,
	)
	if err != nil {
		return equipment.Batch{}, err
	}
	if model.HourlyRateKopecks != hourlyRateKopecks {
		return equipment.Batch{}, equipment.ErrModelRateConflict
	}

	const allocateQuery = `
		UPDATE equipment_models
		SET next_sequence = next_sequence + $2
		WHERE id = $1
		RETURNING next_sequence - $2
	`
	var firstSequence int64
	if err := tx.QueryRow(ctx, allocateQuery, model.ID, input.Quantity).Scan(&firstSequence); err != nil {
		return equipment.Batch{}, fmt.Errorf("allocate equipment sequence range: %w", err)
	}
	lastSequence := firstSequence + int64(input.Quantity) - 1

	const insertQuery = `
		WITH inserted AS (
			INSERT INTO equipment (model_id, sequence_number, status)
			SELECT $1, number, 'available'
			FROM generate_series($2::bigint, $3::bigint) AS number
			RETURNING id, model_id, sequence_number, status
		)
		SELECT id, model_id, sequence_number, status
		FROM inserted
		ORDER BY sequence_number
	`
	rows, err := tx.Query(ctx, insertQuery, model.ID, firstSequence, lastSequence)
	if err != nil {
		return equipment.Batch{}, fmt.Errorf("insert equipment batch: %w", err)
	}
	items := make([]equipment.Item, 0, input.Quantity)
	for rows.Next() {
		var item equipment.Item
		if err := rows.Scan(&item.ID, &item.ModelID, &item.SequenceNumber, &item.Status); err != nil {
			rows.Close()
			return equipment.Batch{}, fmt.Errorf("scan created equipment: %w", err)
		}
		item.Kind = model.Kind
		item.ModelCode = model.ModelCode
		item.HourlyRateKopecks = model.HourlyRateKopecks
		if err := populateInventoryNumber(&item); err != nil {
			rows.Close()
			return equipment.Batch{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return equipment.Batch{}, fmt.Errorf("iterate created equipment: %w", err)
	}
	rows.Close()
	if len(items) != input.Quantity {
		return equipment.Batch{}, fmt.Errorf("created %d equipment items, want %d", len(items), input.Quantity)
	}

	batch := equipment.Batch{
		Items:                items,
		FirstInventoryNumber: items[0].InventoryNumber,
		LastInventoryNumber:  items[len(items)-1].InventoryNumber,
	}
	if err := r.writeAudit(
		ctx,
		tx,
		actionEquipmentBatchCreated,
		actor,
		items[0],
		equipmentAuditDetails{Batch: &equipmentBatchAuditDetails{
			Kind:                 input.Kind,
			ModelCode:            model.ModelCode,
			HourlyRateKopecks:    model.HourlyRateKopecks,
			Quantity:             input.Quantity,
			FirstInventoryNumber: batch.FirstInventoryNumber,
			LastInventoryNumber:  batch.LastInventoryNumber,
		}},
	); err != nil {
		return equipment.Batch{}, fmt.Errorf("write create equipment batch audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return equipment.Batch{}, fmt.Errorf("commit create equipment batch transaction: %w", err)
	}
	return batch, nil
}

type equipmentModel struct {
	ID                int64
	Kind              equipment.Kind
	ModelCode         string
	HourlyRateKopecks int64
}

func lockOrCreateEquipmentModel(
	ctx context.Context,
	tx pgx.Tx,
	kind equipment.Kind,
	modelCode string,
	hourlyRateKopecks int64,
) (equipmentModel, error) {
	const insertQuery = `
		INSERT INTO equipment_models (kind, model_code, hourly_rate_kopecks)
		VALUES ($1, $2, $3)
		ON CONFLICT (kind, model_code) DO NOTHING
		RETURNING id, kind, model_code, hourly_rate_kopecks
	`
	var model equipmentModel
	err := tx.QueryRow(ctx, insertQuery, kind, modelCode, hourlyRateKopecks).Scan(
		&model.ID, &model.Kind, &model.ModelCode, &model.HourlyRateKopecks,
	)
	if err == nil {
		return model, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return equipmentModel{}, fmt.Errorf("insert equipment model: %w", err)
	}

	const selectQuery = `
		SELECT id, kind, model_code, hourly_rate_kopecks
		FROM equipment_models
		WHERE kind = $1 AND model_code = $2
		FOR UPDATE
	`
	err = tx.QueryRow(ctx, selectQuery, kind, modelCode).Scan(
		&model.ID, &model.Kind, &model.ModelCode, &model.HourlyRateKopecks,
	)
	if err != nil {
		return equipmentModel{}, fmt.Errorf("lock equipment model: %w", err)
	}
	return model, nil
}

// List возвращает всё оборудование от недавно добавленного к более раннему.
func (r *EquipmentRepository) List(ctx context.Context) ([]equipment.Item, error) {
	rows, err := r.pool.Query(ctx, equipmentSelect+` ORDER BY e.id DESC`)
	if err != nil {
		return nil, fmt.Errorf("query equipment: %w", err)
	}
	return scanEquipmentRows(rows, "equipment")
}

// ListPage возвращает одну страницу действующего или списанного оборудования.
func (r *EquipmentRepository) ListPage(
	ctx context.Context,
	input equipment.ListPageInput,
) (equipment.ListPage, error) {
	condition := "e.status <> 'retired'"
	if input.Scope == equipment.ListScopeRetired {
		condition = "e.status = 'retired'"
	}

	var total int
	if err := r.pool.QueryRow(ctx, "SELECT count(*) FROM equipment AS e WHERE "+condition).Scan(&total); err != nil {
		return equipment.ListPage{}, fmt.Errorf("count equipment page: %w", err)
	}
	rows, err := r.pool.Query(
		ctx,
		equipmentSelect+` WHERE `+condition+` ORDER BY e.id DESC LIMIT $1 OFFSET $2`,
		input.PageSize,
		(input.Page-1)*input.PageSize,
	)
	if err != nil {
		return equipment.ListPage{}, fmt.Errorf("query equipment page: %w", err)
	}
	items, err := scanEquipmentRows(rows, "equipment page")
	if err != nil {
		return equipment.ListPage{}, err
	}
	return equipment.ListPage{
		Scope: input.Scope, Items: items, Total: total,
		Page: input.Page, PageSize: input.PageSize,
	}, nil
}

// Get возвращает оборудование по ID.
func (r *EquipmentRepository) Get(ctx context.Context, id int64) (equipment.Item, error) {
	item, err := r.queryEquipment(ctx, equipmentSelect+` WHERE e.id = $1`, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return equipment.Item{}, equipment.ErrEquipmentNotFound
	}
	if err != nil {
		return equipment.Item{}, fmt.Errorf("get equipment: %w", err)
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
		WITH updated AS (
			UPDATE equipment SET status = $1 WHERE id = $2
			RETURNING id, model_id, sequence_number, status
		)
		SELECT u.id, u.model_id, m.model_code, u.sequence_number,
		       m.kind, m.hourly_rate_kopecks, u.status
		FROM updated AS u
		JOIN equipment_models AS m ON m.id = u.model_id
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
	if err := r.writeAudit(ctx, tx, action, actor, item, equipmentAuditDetails{
		Before: equipmentAuditSnapshotFor(before), After: equipmentAuditSnapshotFor(item),
	}); err != nil {
		return equipment.Item{}, fmt.Errorf("write equipment status audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return equipment.Item{}, fmt.Errorf("commit update equipment status transaction: %w", err)
	}
	return item, nil
}

// ChangeModel атомарно выделяет следующий номер целевой модели, переносит
// физическую единицу и сохраняет старые и новые сведения в audit log.
func (r *EquipmentRepository) ChangeModel(
	ctx context.Context,
	actor user.User,
	id int64,
	input equipment.ModelChangeInput,
) (equipment.Item, error) {
	hourlyRateKopecks, err := equipment.HourlyRateKopecks(input.HourlyRateRubles)
	if err != nil {
		return equipment.Item{}, err
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return equipment.Item{}, fmt.Errorf("begin change equipment model transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	before, err := lockEquipment(ctx, tx, id)
	if err != nil {
		return equipment.Item{}, err
	}
	if !before.Status.CanEditDetails() {
		return equipment.Item{}, equipment.ErrEquipmentUpdateNotAllowed
	}
	if before.Kind == input.Kind && before.ModelCode == input.ModelCode {
		return equipment.Item{}, equipment.ErrEquipmentModelUnchanged
	}
	targetModel, err := lockOrCreateEquipmentModel(
		ctx, tx, input.Kind, input.ModelCode, hourlyRateKopecks,
	)
	if err != nil {
		return equipment.Item{}, err
	}
	if targetModel.HourlyRateKopecks != hourlyRateKopecks {
		return equipment.Item{}, equipment.ErrModelRateConflict
	}

	const allocateQuery = `
		UPDATE equipment_models
		SET next_sequence = next_sequence + 1
		WHERE id = $1
		RETURNING next_sequence - 1
	`
	var sequenceNumber int64
	if err := tx.QueryRow(ctx, allocateQuery, targetModel.ID).Scan(&sequenceNumber); err != nil {
		return equipment.Item{}, fmt.Errorf("allocate target equipment sequence: %w", err)
	}

	const updateQuery = `
		UPDATE equipment
		SET model_id = $1, sequence_number = $2
		WHERE id = $3
	`
	commandTag, err := tx.Exec(ctx, updateQuery, targetModel.ID, sequenceNumber, id)
	if err != nil {
		return equipment.Item{}, fmt.Errorf("move equipment to model: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return equipment.Item{}, equipment.ErrEquipmentNotFound
	}

	after, err := queryEquipment(ctx, tx, equipmentSelect+` WHERE e.id = $1`, id)
	if err != nil {
		return equipment.Item{}, fmt.Errorf("get equipment after model change: %w", err)
	}
	if err := r.writeAudit(ctx, tx, actionEquipmentModelChanged, actor, after, equipmentAuditDetails{
		Before: equipmentAuditSnapshotFor(before), After: equipmentAuditSnapshotFor(after),
	}); err != nil {
		return equipment.Item{}, fmt.Errorf("write equipment model audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return equipment.Item{}, fmt.Errorf("commit change equipment model transaction: %w", err)
	}
	return after, nil
}

// ChangeModelRate атомарно изменяет общий тариф модели и записывает количество
// физических единиц, на которые распространяется новое значение.
func (r *EquipmentRepository) ChangeModelRate(
	ctx context.Context,
	actor user.User,
	id int64,
	hourlyRateKopecks int64,
) (equipment.ModelRateChange, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return equipment.ModelRateChange{}, fmt.Errorf("begin change equipment model rate transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	before, err := lockEquipment(ctx, tx, id)
	if err != nil {
		return equipment.ModelRateChange{}, err
	}
	if !before.Status.CanEditDetails() {
		return equipment.ModelRateChange{}, equipment.ErrEquipmentUpdateNotAllowed
	}
	const lockModelQuery = `
		SELECT kind, model_code, hourly_rate_kopecks
		FROM equipment_models
		WHERE id = $1
		FOR UPDATE
	`
	if err := tx.QueryRow(ctx, lockModelQuery, before.ModelID).Scan(
		&before.Kind, &before.ModelCode, &before.HourlyRateKopecks,
	); err != nil {
		return equipment.ModelRateChange{}, fmt.Errorf("lock equipment model for rate change: %w", err)
	}
	if before.HourlyRateKopecks == hourlyRateKopecks {
		return equipment.ModelRateChange{}, equipment.ErrModelRateUnchanged
	}
	const updateQuery = `
		UPDATE equipment_models
		SET hourly_rate_kopecks = $1
		WHERE id = $2
		RETURNING hourly_rate_kopecks
	`
	var updatedRate int64
	if err := tx.QueryRow(ctx, updateQuery, hourlyRateKopecks, before.ModelID).Scan(&updatedRate); err != nil {
		return equipment.ModelRateChange{}, fmt.Errorf("update equipment model rate: %w", err)
	}

	var affectedItems int
	if err := tx.QueryRow(
		ctx,
		`SELECT count(*) FROM equipment WHERE model_id = $1`,
		before.ModelID,
	).Scan(&affectedItems); err != nil {
		return equipment.ModelRateChange{}, fmt.Errorf("count equipment using model rate: %w", err)
	}

	after := before
	after.HourlyRateKopecks = updatedRate
	if err := r.writeAudit(ctx, tx, actionEquipmentRateChanged, actor, after, equipmentAuditDetails{
		ModelRate: &equipmentModelRateAuditDetails{
			ModelID: before.ModelID, Kind: before.Kind, ModelCode: before.ModelCode,
			BeforeKopecks: before.HourlyRateKopecks, AfterKopecks: updatedRate,
			AffectedItems: affectedItems,
		},
	}); err != nil {
		return equipment.ModelRateChange{}, fmt.Errorf("write equipment model rate audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return equipment.ModelRateChange{}, fmt.Errorf("commit change equipment model rate transaction: %w", err)
	}
	return equipment.ModelRateChange{Item: after, AffectedItems: affectedItems}, nil
}

// Delete безвозвратно удаляет списанное оборудование по ID.
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
	commandTag, err := tx.Exec(ctx, `DELETE FROM equipment WHERE id = $1`, id)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == foreignKeyViolationCode {
			return equipment.Item{}, equipment.ErrEquipmentHasHistory
		}
		return equipment.Item{}, fmt.Errorf("delete equipment: %w", err)
	}
	if commandTag.RowsAffected() == 0 {
		return equipment.Item{}, equipment.ErrEquipmentNotFound
	}
	if err := r.writeAudit(ctx, tx, actionEquipmentDeleted, actor, item,
		equipmentAuditDetails{Before: equipmentAuditSnapshotFor(item)}); err != nil {
		return equipment.Item{}, fmt.Errorf("write delete equipment audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return equipment.Item{}, fmt.Errorf("commit delete equipment transaction: %w", err)
	}
	return item, nil
}

const equipmentSelect = `
	SELECT e.id, e.model_id, m.model_code, e.sequence_number,
	       m.kind, m.hourly_rate_kopecks, e.status
	FROM equipment AS e
	JOIN equipment_models AS m ON m.id = e.model_id
`

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
		&item.ModelID,
		&item.ModelCode,
		&item.SequenceNumber,
		&item.Kind,
		&item.HourlyRateKopecks,
		&item.Status,
	)
	if err != nil {
		return equipment.Item{}, err
	}
	if err := populateInventoryNumber(&item); err != nil {
		return equipment.Item{}, err
	}
	return item, nil
}

func scanEquipmentRows(rows pgx.Rows, label string) ([]equipment.Item, error) {
	defer rows.Close()
	items := make([]equipment.Item, 0)
	for rows.Next() {
		var item equipment.Item
		if err := rows.Scan(
			&item.ID, &item.ModelID, &item.ModelCode, &item.SequenceNumber,
			&item.Kind, &item.HourlyRateKopecks, &item.Status,
		); err != nil {
			return nil, fmt.Errorf("scan %s: %w", label, err)
		}
		if err := populateInventoryNumber(&item); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate %s: %w", label, err)
	}
	return items, nil
}

func populateInventoryNumber(item *equipment.Item) error {
	number, err := equipment.InventoryNumber(item.Kind, item.ModelCode, item.SequenceNumber)
	if err != nil {
		return fmt.Errorf("build equipment inventory number: %w", err)
	}
	item.InventoryNumber = number
	return nil
}

func lockEquipment(ctx context.Context, tx pgx.Tx, id int64) (equipment.Item, error) {
	query := equipmentSelect + ` WHERE e.id = $1 FOR UPDATE OF e`
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
	InventoryNumber   string           `json:"inventory_number"`
	Kind              equipment.Kind   `json:"kind"`
	ModelCode         string           `json:"model_code"`
	HourlyRateKopecks int64            `json:"hourly_rate_kopecks"`
	Status            equipment.Status `json:"status"`
}

type equipmentBatchAuditDetails struct {
	Kind                 equipment.Kind `json:"kind"`
	ModelCode            string         `json:"model_code"`
	HourlyRateKopecks    int64          `json:"hourly_rate_kopecks"`
	Quantity             int            `json:"quantity"`
	FirstInventoryNumber string         `json:"first_inventory_number"`
	LastInventoryNumber  string         `json:"last_inventory_number"`
}

type equipmentModelRateAuditDetails struct {
	ModelID       int64          `json:"model_id"`
	Kind          equipment.Kind `json:"kind"`
	ModelCode     string         `json:"model_code"`
	BeforeKopecks int64          `json:"before_kopecks"`
	AfterKopecks  int64          `json:"after_kopecks"`
	AffectedItems int            `json:"affected_items"`
}

type equipmentAuditDetails struct {
	Before    *equipmentAuditSnapshot         `json:"before,omitempty"`
	After     *equipmentAuditSnapshot         `json:"after,omitempty"`
	Batch     *equipmentBatchAuditDetails     `json:"batch,omitempty"`
	ModelRate *equipmentModelRateAuditDetails `json:"model_rate,omitempty"`
}

func equipmentAuditSnapshotFor(item equipment.Item) *equipmentAuditSnapshot {
	return &equipmentAuditSnapshot{
		InventoryNumber:   item.InventoryNumber,
		Kind:              item.Kind,
		ModelCode:         item.ModelCode,
		HourlyRateKopecks: item.HourlyRateKopecks,
		Status:            item.Status,
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
	targetLabel := target.InventoryNumber
	targetType := "equipment"
	targetID := target.ID
	if details.Batch != nil {
		targetLabel = details.Batch.FirstInventoryNumber + " — " + details.Batch.LastInventoryNumber
	}
	if details.ModelRate != nil {
		targetType = "equipment_model"
		targetID = details.ModelRate.ModelID
		prefix, err := details.ModelRate.Kind.InventoryPrefix()
		if err != nil {
			return fmt.Errorf("build equipment model audit label: %w", err)
		}
		targetLabel = prefix + "-" + details.ModelRate.ModelCode
	}
	const query = `
		INSERT INTO audit_events (
			actor_user_id, actor_login, actor_role, action,
			target_type, target_id, target_label, result, details
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'success', $8::jsonb)
	`
	if _, err := tx.Exec(
		ctx, query, actor.ID, actor.Login, actor.Role, action, targetType,
		targetID, targetLabel, encodedDetails,
	); err != nil {
		return fmt.Errorf("insert equipment audit event: %w", err)
	}
	return nil
}
