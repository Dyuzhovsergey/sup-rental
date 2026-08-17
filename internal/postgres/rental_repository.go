package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	rentalClientForeignKeyConstraint = "rentals_client_id_fkey"
	rentalItemForeignKeyConstraint   = "rental_items_equipment_id_fkey"
	rentalItemUniqueEquipmentKey     = "rental_items_rental_equipment_key"
	actionRentalConfirmed            = "rental.confirmed"
	actionRentalIssued               = "rental.issued"
	actionRentalCancelled            = "rental.cancelled"
)

type rentalAuditDetails struct {
	ClientID       int64      `json:"client_id"`
	PlannedStart   time.Time  `json:"planned_start"`
	PlannedEnd     time.Time  `json:"planned_end"`
	EquipmentCount int        `json:"equipment_count"`
	IssuedAt       *time.Time `json:"issued_at,omitempty"`
}

// Issue атомарно переводит подтверждённую аренду в active, отмечает весь её
// состав как issued и сохраняет обязательный audit event.
func (r *RentalRepository) Issue(
	ctx context.Context,
	actor user.User,
	id int64,
	issuedAt time.Time,
) (rental.Rental, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("begin issue rental transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const rentalQuery = `
		SELECT client_id, planned_start_at, planned_end_at, status, issued_at
		FROM rentals
		WHERE id = $1
		FOR UPDATE
	`
	var (
		clientID       int64
		plannedStart   time.Time
		plannedEnd     time.Time
		status         rental.Status
		storedIssuedAt *time.Time
	)
	if err := tx.QueryRow(ctx, rentalQuery, id).Scan(
		&clientID, &plannedStart, &plannedEnd, &status, &storedIssuedAt,
	); errors.Is(err, pgx.ErrNoRows) {
		return rental.Rental{}, rental.ErrRentalNotFound
	} else if err != nil {
		return rental.Rental{}, fmt.Errorf("lock rental for issue: %w", err)
	}
	if status != rental.StatusConfirmed {
		return rental.Rental{}, fmt.Errorf(
			"%w: %s -> %s", rental.ErrStatusTransitionNotAllowed, status, rental.StatusActive,
		)
	}

	type lockedItem struct {
		position int
		item     rental.Item
		status   equipment.Status
	}
	const itemsQuery = `
		SELECT ri.position, ri.equipment_id, ri.inventory_number,
		       ri.kind, ri.model_code, ri.hourly_rate_kopecks, e.status
		FROM rental_items AS ri
		JOIN equipment AS e ON e.id = ri.equipment_id
		WHERE ri.rental_id = $1
		ORDER BY e.id
		FOR UPDATE OF e
	`
	rows, err := tx.Query(ctx, itemsQuery, id)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("lock rental equipment for issue: %w", err)
	}
	locked := make([]lockedItem, 0)
	for rows.Next() {
		var value lockedItem
		if err := rows.Scan(
			&value.position,
			&value.item.EquipmentID,
			&value.item.InventoryNumber,
			&value.item.Kind,
			&value.item.ModelCode,
			&value.item.HourlyRateKopecks,
			&value.status,
		); err != nil {
			rows.Close()
			return rental.Rental{}, fmt.Errorf("scan rental equipment for issue: %w", err)
		}
		if value.status != equipment.StatusAvailable {
			rows.Close()
			return rental.Rental{}, rental.ErrEquipmentUnavailable
		}
		locked = append(locked, value)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return rental.Rental{}, fmt.Errorf("iterate rental equipment for issue: %w", err)
	}
	rows.Close()
	sort.Slice(locked, func(i, j int) bool { return locked[i].position < locked[j].position })
	items := make([]rental.Item, 0, len(locked))
	for _, value := range locked {
		items = append(items, value.item)
	}

	interval, err := rental.NewInterval(plannedStart, plannedEnd)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("restore rental interval for issue: %w", err)
	}
	value, err := rental.Restore(id, clientID, interval, status, storedIssuedAt, items)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("restore rental for issue: %w", err)
	}
	if err := value.Issue(issuedAt); err != nil {
		return rental.Rental{}, err
	}

	const updateRentalQuery = `
		UPDATE rentals
		SET status = 'active', issued_at = $2
		WHERE id = $1
	`
	if _, err := tx.Exec(ctx, updateRentalQuery, id, issuedAt); err != nil {
		return rental.Rental{}, fmt.Errorf("mark rental issued: %w", err)
	}
	const updateEquipmentQuery = `
		UPDATE equipment AS e
		SET status = 'issued'
		FROM rental_items AS ri
		WHERE ri.rental_id = $1
		  AND ri.equipment_id = e.id
		  AND e.status = 'available'
	`
	result, err := tx.Exec(ctx, updateEquipmentQuery, id)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("mark rental equipment issued: %w", err)
	}
	if result.RowsAffected() != int64(len(items)) {
		return rental.Rental{}, rental.ErrEquipmentUnavailable
	}

	if err := r.writeAudit(
		ctx, tx, actionRentalIssued, actor, value,
		rentalAuditDetails{
			ClientID: clientID, PlannedStart: plannedStart, PlannedEnd: plannedEnd,
			EquipmentCount: len(items), IssuedAt: &issuedAt,
		},
	); err != nil {
		return rental.Rental{}, fmt.Errorf("write issued rental audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return rental.Rental{}, fmt.Errorf("commit issue rental transaction: %w", err)
	}
	return value, nil
}

// Cancel атомарно отменяет подтверждённую аренду и сохраняет обязательный
// audit event. Состав остаётся в истории, а его резервирование прекращается.
func (r *RentalRepository) Cancel(
	ctx context.Context,
	actor user.User,
	id int64,
) (rental.Rental, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("begin cancel rental transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const rentalQuery = `
		SELECT client_id, planned_start_at, planned_end_at, status, issued_at
		FROM rentals
		WHERE id = $1
		FOR UPDATE
	`
	var (
		clientID     int64
		plannedStart time.Time
		plannedEnd   time.Time
		status       rental.Status
		issuedAt     *time.Time
	)
	if err := tx.QueryRow(ctx, rentalQuery, id).Scan(
		&clientID, &plannedStart, &plannedEnd, &status, &issuedAt,
	); errors.Is(err, pgx.ErrNoRows) {
		return rental.Rental{}, rental.ErrRentalNotFound
	} else if err != nil {
		return rental.Rental{}, fmt.Errorf("lock rental for cancellation: %w", err)
	}
	if status != rental.StatusConfirmed {
		return rental.Rental{}, fmt.Errorf(
			"%w: %s -> %s", rental.ErrStatusTransitionNotAllowed, status, rental.StatusCancelled,
		)
	}

	const itemsQuery = `
		SELECT equipment_id, inventory_number, kind, model_code, hourly_rate_kopecks
		FROM rental_items
		WHERE rental_id = $1
		ORDER BY position
	`
	rows, err := tx.Query(ctx, itemsQuery, id)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("query rental items for cancellation: %w", err)
	}
	items := make([]rental.Item, 0)
	for rows.Next() {
		var item rental.Item
		if err := rows.Scan(
			&item.EquipmentID,
			&item.InventoryNumber,
			&item.Kind,
			&item.ModelCode,
			&item.HourlyRateKopecks,
		); err != nil {
			rows.Close()
			return rental.Rental{}, fmt.Errorf("scan rental item for cancellation: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return rental.Rental{}, fmt.Errorf("iterate rental items for cancellation: %w", err)
	}
	rows.Close()

	interval, err := rental.NewInterval(plannedStart, plannedEnd)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("restore rental interval for cancellation: %w", err)
	}
	value, err := rental.Restore(id, clientID, interval, status, issuedAt, items)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("restore rental for cancellation: %w", err)
	}
	if err := value.Cancel(); err != nil {
		return rental.Rental{}, err
	}

	if _, err := tx.Exec(ctx, "UPDATE rentals SET status = 'cancelled' WHERE id = $1", id); err != nil {
		return rental.Rental{}, fmt.Errorf("mark rental cancelled: %w", err)
	}
	if err := r.writeAudit(
		ctx, tx, actionRentalCancelled, actor, value,
		rentalAuditDetails{
			ClientID: clientID, PlannedStart: plannedStart, PlannedEnd: plannedEnd,
			EquipmentCount: len(items),
		},
	); err != nil {
		return rental.Rental{}, fmt.Errorf("write cancelled rental audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return rental.Rental{}, fmt.Errorf("commit cancel rental transaction: %w", err)
	}
	return value, nil
}

type rentalAuditWriter func(
	context.Context,
	pgx.Tx,
	string,
	user.User,
	rental.Rental,
	rentalAuditDetails,
) error

// RentalRepository хранит аренды и их упорядоченный состав в PostgreSQL.
type RentalRepository struct {
	pool       *pgxpool.Pool
	writeAudit rentalAuditWriter
}

// NewRentalRepository создаёт PostgreSQL repository аренды.
func NewRentalRepository(pool *pgxpool.Pool) *RentalRepository {
	return &RentalRepository{pool: pool, writeAudit: writeRentalAudit}
}

// CreateConfirmed повторно проверяет доступность, блокирует выбранные
// физические единицы и атомарно сохраняет подтверждённую аренду вместе с audit event.
func (r *RentalRepository) CreateConfirmed(
	ctx context.Context,
	actor user.User,
	clientID int64,
	interval rental.Interval,
	selections []rental.ModelSelection,
) (rental.Rental, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("begin create confirmed rental transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	items, err := lockAvailableRentalEquipment(ctx, tx, interval, selections)
	if err != nil {
		return rental.Rental{}, err
	}
	value, err := rental.New(clientID, interval, items)
	if err != nil {
		return rental.Rental{}, err
	}

	const insertRentalQuery = `
		INSERT INTO rentals (client_id, planned_start_at, planned_end_at, status)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`
	var id int64
	if err := tx.QueryRow(
		ctx,
		insertRentalQuery,
		value.ClientID,
		value.Interval.Start(),
		value.Interval.End(),
		value.Status,
	).Scan(&id); err != nil {
		if mapped := mapRentalReferenceError(err); mapped != nil {
			return rental.Rental{}, mapped
		}
		return rental.Rental{}, fmt.Errorf("insert rental: %w", err)
	}

	if err := insertRentalItems(ctx, tx, id, value.Items()); err != nil {
		return rental.Rental{}, err
	}
	created, err := rental.Restore(
		id, value.ClientID, value.Interval, value.Status, nil, value.Items(),
	)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("restore confirmed rental: %w", err)
	}
	if err := r.writeAudit(
		ctx,
		tx,
		actionRentalConfirmed,
		actor,
		created,
		rentalAuditDetails{
			ClientID: clientID, PlannedStart: interval.Start(), PlannedEnd: interval.End(),
			EquipmentCount: created.ItemCount(),
		},
	); err != nil {
		return rental.Rental{}, fmt.Errorf("write confirmed rental audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return rental.Rental{}, fmt.Errorf("commit create confirmed rental transaction: %w", err)
	}
	return created, nil
}

// Get возвращает аренду с составом в сохранённом порядке.
func (r *RentalRepository) Get(ctx context.Context, id int64) (rental.Rental, error) {
	if id <= 0 {
		return rental.Rental{}, rental.ErrInvalidRentalID
	}

	const query = `
		SELECT r.id, r.client_id, r.planned_start_at, r.planned_end_at, r.status,
		       r.issued_at,
		       ri.equipment_id,
		       COALESCE(ri.inventory_number, ''),
		       COALESCE(ri.kind, ''),
		       COALESCE(ri.model_code, ''),
		       COALESCE(ri.hourly_rate_kopecks, 0)
		FROM rentals AS r
		LEFT JOIN rental_items AS ri ON ri.rental_id = r.id
		WHERE r.id = $1
		ORDER BY ri.position
	`
	rows, err := r.pool.Query(ctx, query, id)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("query rental: %w", err)
	}
	defer rows.Close()

	var (
		found    bool
		clientID int64
		start    time.Time
		end      time.Time
		status   rental.Status
		issuedAt *time.Time
		items    []rental.Item
	)
	for rows.Next() {
		var (
			rowID             int64
			equipmentID       *int64
			inventoryNumber   string
			kind              equipment.Kind
			modelCode         string
			hourlyRateKopecks int64
		)
		if err := rows.Scan(
			&rowID,
			&clientID,
			&start,
			&end,
			&status,
			&issuedAt,
			&equipmentID,
			&inventoryNumber,
			&kind,
			&modelCode,
			&hourlyRateKopecks,
		); err != nil {
			return rental.Rental{}, fmt.Errorf("scan rental: %w", err)
		}
		found = true
		if equipmentID != nil {
			items = append(items, rental.Item{
				EquipmentID:       *equipmentID,
				InventoryNumber:   inventoryNumber,
				Kind:              kind,
				ModelCode:         modelCode,
				HourlyRateKopecks: hourlyRateKopecks,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return rental.Rental{}, fmt.Errorf("iterate rental: %w", err)
	}
	if !found {
		return rental.Rental{}, rental.ErrRentalNotFound
	}

	interval, err := rental.NewInterval(start, end)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("restore rental interval: %w", err)
	}
	restored, err := rental.Restore(id, clientID, interval, status, issuedAt, items)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("restore rental: %w", err)
	}
	return restored, nil
}

// ListPage возвращает страницу аренд от новых записей к старым вместе с
// текущим ФИО клиента и предварительной стоимостью по сохранённым снимкам.
func (r *RentalRepository) ListPage(
	ctx context.Context,
	statuses []rental.Status,
	page, pageSize int,
) (rental.Page, error) {
	statusValues := make([]string, 0, len(statuses))
	for _, status := range statuses {
		statusValues = append(statusValues, string(status))
	}
	var total int
	if err := r.pool.QueryRow(
		ctx,
		"SELECT count(*) FROM rentals WHERE status = ANY($1::text[])",
		statusValues,
	).Scan(&total); err != nil {
		return rental.Page{}, fmt.Errorf("count rentals: %w", err)
	}

	const query = `
		SELECT r.id, r.client_id, c.full_name,
		       r.planned_start_at, r.planned_end_at, r.status,
		       count(ri.equipment_id),
		       COALESCE(sum(ri.hourly_rate_kopecks), 0)
		FROM rentals AS r
		JOIN clients AS c ON c.id = r.client_id
		LEFT JOIN rental_items AS ri ON ri.rental_id = r.id
		WHERE r.status = ANY($3::text[])
		GROUP BY r.id, c.full_name
		ORDER BY r.id DESC
		LIMIT $1 OFFSET $2
	`
	rows, err := r.pool.Query(ctx, query, pageSize, (page-1)*pageSize, statusValues)
	if err != nil {
		return rental.Page{}, fmt.Errorf("query rental page: %w", err)
	}
	defer rows.Close()

	summaries := make([]rental.Summary, 0, pageSize)
	for rows.Next() {
		var (
			summary       rental.Summary
			start         time.Time
			end           time.Time
			hourlyRateSum int64
		)
		if err := rows.Scan(
			&summary.ID,
			&summary.ClientID,
			&summary.ClientName,
			&start,
			&end,
			&summary.Status,
			&summary.ItemCount,
			&hourlyRateSum,
		); err != nil {
			return rental.Page{}, fmt.Errorf("scan rental page: %w", err)
		}
		if !summary.Status.Valid() {
			return rental.Page{}, fmt.Errorf("scan rental page: %w", rental.ErrInvalidStatus)
		}
		interval, err := rental.NewInterval(start, end)
		if err != nil {
			return rental.Page{}, fmt.Errorf("restore rental page interval: %w", err)
		}
		summary.Interval = interval
		halfHourlyRate := hourlyRateSum / 2
		if halfHourlyRate > 0 && int64(interval.SlotCount()) > math.MaxInt64/halfHourlyRate {
			return rental.Page{}, fmt.Errorf("calculate rental page total: %w", rental.ErrPriceOverflow)
		}
		summary.PlannedTotalKopecks = halfHourlyRate * int64(interval.SlotCount())
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return rental.Page{}, fmt.Errorf("iterate rental page: %w", err)
	}

	return rental.Page{
		Rentals:  summaries,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

// AvailableEquipment возвращает физические единицы в состоянии available,
// которые не пересекаются с confirmed или active арендой на весь переданный
// полуоткрытый интервал.
func (r *RentalRepository) AvailableEquipment(
	ctx context.Context,
	interval rental.Interval,
) ([]equipment.Item, error) {
	const query = `
		SELECT e.id, e.model_id, e.sequence_number,
		       m.kind, m.model_code, m.hourly_rate_kopecks, e.status
		FROM equipment AS e
		JOIN equipment_models AS m ON m.id = e.model_id
		WHERE e.status = 'available'
		  AND NOT EXISTS (
		      SELECT 1
		      FROM rental_items AS ri
		      JOIN rentals AS r ON r.id = ri.rental_id
		      WHERE ri.equipment_id = e.id
		        AND r.status IN ('confirmed', 'active')
		        AND r.planned_start_at < $2
		        AND $1 < r.planned_end_at
		  )
		ORDER BY m.kind, m.model_code, e.id
	`
	rows, err := r.pool.Query(ctx, query, interval.Start(), interval.End())
	if err != nil {
		return nil, fmt.Errorf("query available rental equipment: %w", err)
	}
	defer rows.Close()

	items := make([]equipment.Item, 0)
	for rows.Next() {
		var item equipment.Item
		if err := rows.Scan(
			&item.ID,
			&item.ModelID,
			&item.SequenceNumber,
			&item.Kind,
			&item.ModelCode,
			&item.HourlyRateKopecks,
			&item.Status,
		); err != nil {
			return nil, fmt.Errorf("scan available rental equipment: %w", err)
		}
		if err := populateInventoryNumber(&item); err != nil {
			return nil, fmt.Errorf("build available rental equipment number: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate available rental equipment: %w", err)
	}
	return items, nil
}

func lockAvailableRentalEquipment(
	ctx context.Context,
	tx pgx.Tx,
	interval rental.Interval,
	selections []rental.ModelSelection,
) ([]rental.Item, error) {
	ordered := append([]rental.ModelSelection(nil), selections...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ModelID < ordered[j].ModelID })

	const query = `
		SELECT e.id, m.kind, m.model_code, m.hourly_rate_kopecks, e.sequence_number
		FROM equipment AS e
		JOIN equipment_models AS m ON m.id = e.model_id
		WHERE e.model_id = $1
		  AND e.status = 'available'
		  AND NOT EXISTS (
		      SELECT 1
		      FROM rental_items AS ri
		      JOIN rentals AS r ON r.id = ri.rental_id
		      WHERE ri.equipment_id = e.id
		        AND r.status IN ('confirmed', 'active')
		        AND r.planned_start_at < $3
		        AND $2 < r.planned_end_at
		  )
		ORDER BY e.id
		LIMIT $4
		FOR UPDATE OF e SKIP LOCKED
	`

	lockedByModel := make(map[int64][]rental.Item, len(ordered))
	for _, selection := range ordered {
		if selection.Quantity == 0 {
			continue
		}
		rows, err := tx.Query(
			ctx, query, selection.ModelID, interval.Start(), interval.End(), selection.Quantity,
		)
		if err != nil {
			return nil, fmt.Errorf("lock available rental equipment: %w", err)
		}
		selectedCount := 0
		for rows.Next() {
			var (
				item     rental.Item
				sequence int64
			)
			if err := rows.Scan(
				&item.EquipmentID,
				&item.Kind,
				&item.ModelCode,
				&item.HourlyRateKopecks,
				&sequence,
			); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan locked rental equipment: %w", err)
			}
			item.InventoryNumber, err = equipment.InventoryNumber(item.Kind, item.ModelCode, sequence)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("build locked rental equipment number: %w", err)
			}
			lockedByModel[selection.ModelID] = append(lockedByModel[selection.ModelID], item)
			selectedCount++
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("iterate locked rental equipment: %w", err)
		}
		rows.Close()
		if selectedCount != selection.Quantity {
			return nil, rental.ErrInsufficientEquipment
		}
	}

	items := make([]rental.Item, 0)
	for _, selection := range selections {
		items = append(items, lockedByModel[selection.ModelID]...)
	}
	return items, nil
}

func insertRentalItems(
	ctx context.Context,
	tx pgx.Tx,
	rentalID int64,
	items []rental.Item,
) error {
	const query = `
		INSERT INTO rental_items (
			rental_id, equipment_id, position, inventory_number,
			kind, model_code, hourly_rate_kopecks
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	for index, item := range items {
		if _, err := tx.Exec(
			ctx,
			query,
			rentalID,
			item.EquipmentID,
			index+1,
			item.InventoryNumber,
			item.Kind,
			item.ModelCode,
			item.HourlyRateKopecks,
		); err != nil {
			if mapped := mapRentalReferenceError(err); mapped != nil {
				return mapped
			}
			if mapped := mapRentalItemConstraintError(err); mapped != nil {
				return mapped
			}
			return fmt.Errorf("insert rental item at position %d: %w", index+1, err)
		}
	}
	return nil
}

func mapRentalReferenceError(err error) error {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != foreignKeyViolationCode {
		return nil
	}
	switch postgresError.ConstraintName {
	case rentalClientForeignKeyConstraint:
		return client.ErrClientNotFound
	case rentalItemForeignKeyConstraint:
		return equipment.ErrEquipmentNotFound
	default:
		return nil
	}
}

func mapRentalItemConstraintError(err error) error {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return nil
	}
	if postgresError.ConstraintName == rentalItemUniqueEquipmentKey {
		return rental.ErrEquipmentAlreadyAdded
	}
	return nil
}

func writeRentalAudit(
	ctx context.Context,
	tx pgx.Tx,
	action string,
	actor user.User,
	target rental.Rental,
	details rentalAuditDetails,
) error {
	encodedDetails, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode rental audit details: %w", err)
	}
	const query = `
		INSERT INTO audit_events (
			actor_user_id, actor_login, actor_role, action,
			target_type, target_id, target_label, result, details
		)
		VALUES ($1, $2, $3, $4, 'rental', $5, $6, 'success', $7::jsonb)
	`
	if _, err := tx.Exec(
		ctx,
		query,
		actor.ID,
		actor.Login,
		actor.Role,
		action,
		target.ID,
		fmt.Sprintf("Аренда №%d", target.ID),
		encodedDetails,
	); err != nil {
		return fmt.Errorf("insert rental audit event: %w", err)
	}
	return nil
}
