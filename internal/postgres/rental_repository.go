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
	actionRentalEquipmentReplaced    = "rental.equipment_replaced"
	actionRentalCancelled            = "rental.cancelled"
	actionRentalCompleted            = "rental.completed"
)

type rentalAuditDetails struct {
	ClientID            int64                    `json:"client_id"`
	PlannedStart        time.Time                `json:"planned_start"`
	PlannedEnd          time.Time                `json:"planned_end"`
	EquipmentCount      int                      `json:"equipment_count"`
	IssuedAt            *time.Time               `json:"issued_at,omitempty"`
	ExpectedReturnAt    *time.Time               `json:"expected_return_at,omitempty"`
	ReturnedAt          *time.Time               `json:"returned_at,omitempty"`
	PlannedTotalKopecks *int64                   `json:"planned_total_kopecks,omitempty"`
	OverdueSlots        *int                     `json:"overdue_slots,omitempty"`
	OverdueTotalKopecks *int64                   `json:"overdue_total_kopecks,omitempty"`
	FinalTotalKopecks   *int64                   `json:"final_total_kopecks,omitempty"`
	Replacements        []rentalReplacementAudit `json:"replacements,omitempty"`
}

type rentalReplacementAudit struct {
	OriginalEquipmentID        int64  `json:"original_equipment_id"`
	OriginalInventoryNumber    string `json:"original_inventory_number"`
	ReplacementEquipmentID     int64  `json:"replacement_equipment_id"`
	ReplacementInventoryNumber string `json:"replacement_inventory_number"`
}

// Complete атомарно переводит активную аренду в completed, возвращает весь её
// состав в available и сохраняет обязательный audit event.
func (r *RentalRepository) Complete(
	ctx context.Context,
	actor user.User,
	id int64,
	returnedAt time.Time,
) (rental.Rental, error) {
	values, err := r.CompleteMany(ctx, actor, []int64{id}, returnedAt)
	if err != nil {
		return rental.Rental{}, err
	}
	return values[0], nil
}

// CompleteMany одной транзакцией завершает все выбранные активные аренды,
// возвращает их оборудование в available и сохраняет отдельный audit event для
// каждой аренды. Ошибка одной позиции откатывает всю группу.
func (r *RentalRepository) CompleteMany(
	ctx context.Context,
	actor user.User,
	ids []int64,
	returnedAt time.Time,
) ([]rental.Rental, error) {
	// PostgreSQL хранит timestamptz с микросекундной точностью. Нормализация до
	// расчёта не позволяет границе оплачиваемого слота измениться после записи.
	returnedAt = returnedAt.UTC().Truncate(time.Microsecond)
	orderedIDs, err := validatedBulkRentalIDs(ids)
	if err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin complete rental selection transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	values := make([]rental.Rental, 0, len(orderedIDs))
	equipmentIDs := make([]int64, 0)
	seenEquipment := make(map[int64]struct{})
	for _, id := range orderedIDs {
		value, err := lockRental(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if value.Status != rental.StatusActive {
			return nil, fmt.Errorf(
				"%w: %s -> %s", rental.ErrStatusTransitionNotAllowed, value.Status, rental.StatusCompleted,
			)
		}
		for _, item := range value.Items() {
			if _, exists := seenEquipment[item.EquipmentID]; exists {
				return nil, rental.ErrEquipmentUnavailable
			}
			seenEquipment[item.EquipmentID] = struct{}{}
			equipmentIDs = append(equipmentIDs, item.EquipmentID)
		}
		if err := value.Complete(returnedAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := lockIssuedEquipment(ctx, tx, equipmentIDs); err != nil {
		return nil, err
	}

	for _, value := range values {
		settlement, ok := value.Settlement()
		if !ok {
			return nil, rental.ErrSettlementNotAvailable
		}
		result, err := tx.Exec(
			ctx,
			`UPDATE rentals
			 SET status = 'completed', returned_at = $2, overdue_slots = $3, final_total_kopecks = $4
			 WHERE id = $1 AND status = 'active'`,
			value.ID,
			returnedAt,
			settlement.OverdueSlots,
			settlement.FinalTotalKopecks,
		)
		if err != nil {
			return nil, fmt.Errorf("mark rental %d completed: %w", value.ID, err)
		}
		if result.RowsAffected() != 1 {
			return nil, rental.ErrStatusTransitionNotAllowed
		}
	}
	result, err := tx.Exec(
		ctx,
		"UPDATE equipment SET status = 'available' WHERE id = ANY($1) AND status = 'issued'",
		equipmentIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("mark selected returned equipment available: %w", err)
	}
	if result.RowsAffected() != int64(len(equipmentIDs)) {
		return nil, rental.ErrEquipmentUnavailable
	}

	for _, value := range values {
		issuedAt, _ := value.IssuedAt()
		expectedReturnAt, _ := value.ExpectedReturnAt()
		settlement, _ := value.Settlement()
		if err := r.writeAudit(
			ctx, tx, actionRentalCompleted, actor, value,
			rentalAuditDetails{
				ClientID: value.ClientID, PlannedStart: value.Interval.Start(), PlannedEnd: value.Interval.End(),
				EquipmentCount: value.ItemCount(), IssuedAt: &issuedAt,
				ExpectedReturnAt: &expectedReturnAt, ReturnedAt: &returnedAt,
				PlannedTotalKopecks: &settlement.PlannedTotalKopecks,
				OverdueSlots:        &settlement.OverdueSlots, OverdueTotalKopecks: &settlement.OverdueTotalKopecks,
				FinalTotalKopecks: &settlement.FinalTotalKopecks,
			},
		); err != nil {
			return nil, fmt.Errorf("write completed rental %d audit event: %w", value.ID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit complete rental selection transaction: %w", err)
	}
	return values, nil
}

// Issue атомарно переводит подтверждённую аренду в active, отмечает весь её
// состав как issued и сохраняет обязательный audit event.
func (r *RentalRepository) Issue(
	ctx context.Context,
	actor user.User,
	id int64,
	issuedAt time.Time,
) (rental.Rental, error) {
	return r.IssueWithReplacements(ctx, actor, id, issuedAt, nil)
}

// IssueWithReplacements выдаёт аренду и атомарно заменяет конфликтующие
// физические единицы на выбранные единицы той же модели.
func (r *RentalRepository) IssueWithReplacements(
	ctx context.Context,
	actor user.User,
	id int64,
	issuedAt time.Time,
	replacements []rental.EquipmentReplacement,
) (rental.Rental, error) {
	issuedAt = issuedAt.UTC().Truncate(time.Microsecond)
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("begin issue rental transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	value, err := lockRental(ctx, tx, id)
	if err != nil {
		return rental.Rental{}, err
	}
	if value.Status != rental.StatusConfirmed {
		return rental.Rental{}, fmt.Errorf("%w: %s -> %s", rental.ErrStatusTransitionNotAllowed, value.Status, rental.StatusActive)
	}
	if err := value.Issue(issuedAt); err != nil {
		return rental.Rental{}, err
	}
	expectedReturnAt, _ := value.ExpectedReturnAt()
	originalItems := value.Items()
	allLockIDs := make([]int64, 0, len(originalItems)+len(replacements))
	for _, item := range originalItems {
		allLockIDs = append(allLockIDs, item.EquipmentID)
	}
	for _, replacement := range replacements {
		allLockIDs = append(allLockIDs, replacement.ReplacementEquipmentID)
	}
	if err := lockEquipmentForIssue(ctx, tx, allLockIDs); err != nil {
		return rental.Rental{}, err
	}

	conflicts, err := conflictingEquipmentIDs(ctx, tx, id, issuedAt, expectedReturnAt, originalItems)
	if err != nil {
		return rental.Rental{}, err
	}
	replacementByOriginal := make(map[int64]int64, len(replacements))
	for _, replacement := range replacements {
		replacementByOriginal[replacement.OriginalEquipmentID] = replacement.ReplacementEquipmentID
	}
	if len(conflicts) != len(replacementByOriginal) {
		return rental.Rental{}, rental.ErrIssueConflict
	}

	updatedItems := append([]rental.Item(nil), originalItems...)
	auditReplacements := make([]rentalReplacementAudit, 0, len(replacements))
	for index, item := range updatedItems {
		_, conflict := conflicts[item.EquipmentID]
		replacementID, supplied := replacementByOriginal[item.EquipmentID]
		if conflict != supplied {
			return rental.Rental{}, rental.ErrInvalidReplacement
		}
		if !conflict {
			continue
		}
		candidate, err := loadReplacementCandidate(ctx, tx, id, item, replacementID, issuedAt, expectedReturnAt)
		if err != nil {
			return rental.Rental{}, err
		}
		result, err := tx.Exec(ctx,
			`UPDATE rental_items SET equipment_id = $3, inventory_number = $4
			 WHERE rental_id = $1 AND equipment_id = $2`,
			id, item.EquipmentID, candidate.ID, candidate.InventoryNumber,
		)
		if err != nil {
			return rental.Rental{}, fmt.Errorf("replace rental equipment: %w", err)
		}
		if result.RowsAffected() != 1 {
			return rental.Rental{}, rental.ErrInvalidReplacement
		}
		updatedItems[index].EquipmentID = candidate.ID
		updatedItems[index].InventoryNumber = candidate.InventoryNumber
		auditReplacements = append(auditReplacements, rentalReplacementAudit{
			OriginalEquipmentID: item.EquipmentID, OriginalInventoryNumber: item.InventoryNumber,
			ReplacementEquipmentID: candidate.ID, ReplacementInventoryNumber: candidate.InventoryNumber,
		})
	}

	restored, err := rental.Restore(id, value.ClientID, value.Interval, rental.StatusConfirmed, nil, nil, updatedItems)
	if err != nil {
		return rental.Rental{}, err
	}
	if err := restored.Issue(issuedAt); err != nil {
		return rental.Rental{}, err
	}
	value = restored
	selectedIDs := make([]int64, 0, len(updatedItems))
	for _, item := range updatedItems {
		selectedIDs = append(selectedIDs, item.EquipmentID)
	}
	if err := markRentalIssued(ctx, tx, value, issuedAt, selectedIDs); err != nil {
		return rental.Rental{}, err
	}
	if len(auditReplacements) > 0 {
		if err := r.writeAudit(ctx, tx, actionRentalEquipmentReplaced, actor, value, rentalAuditDetails{
			ClientID: value.ClientID, PlannedStart: value.Interval.Start(), PlannedEnd: value.Interval.End(),
			EquipmentCount: value.ItemCount(), Replacements: auditReplacements,
		}); err != nil {
			return rental.Rental{}, fmt.Errorf("write rental replacement audit event: %w", err)
		}
	}
	expectedReturnAt, _ = value.ExpectedReturnAt()
	if err := r.writeAudit(ctx, tx, actionRentalIssued, actor, value, rentalAuditDetails{
		ClientID: value.ClientID, PlannedStart: value.Interval.Start(), PlannedEnd: value.Interval.End(),
		EquipmentCount: value.ItemCount(), IssuedAt: &issuedAt, ExpectedReturnAt: &expectedReturnAt,
	}); err != nil {
		return rental.Rental{}, fmt.Errorf("write issued rental audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return rental.Rental{}, fmt.Errorf("commit issue rental transaction: %w", err)
	}
	return value, nil
}

// PreviewIssue рассчитывает фактический период и возвращает конфликты состава
// вместе со свободными заменами той же модели.
func (r *RentalRepository) PreviewIssue(ctx context.Context, id int64, issuedAt time.Time) (rental.IssuePreview, error) {
	value, err := r.Get(ctx, id)
	if err != nil {
		return rental.IssuePreview{}, err
	}
	if value.Status != rental.StatusConfirmed {
		return rental.IssuePreview{}, rental.ErrStatusTransitionNotAllowed
	}
	issuedAt = issuedAt.UTC().Truncate(time.Microsecond)
	if err := value.Issue(issuedAt); err != nil {
		return rental.IssuePreview{}, err
	}
	expectedReturnAt, _ := value.ExpectedReturnAt()
	items := value.Items()
	conflicting, err := conflictingEquipmentIDs(ctx, r.pool, id, issuedAt, expectedReturnAt, items)
	if err != nil {
		return rental.IssuePreview{}, err
	}
	conflicts := make([]rental.IssueConflict, 0, len(conflicting))
	for _, item := range items {
		if _, ok := conflicting[item.EquipmentID]; !ok {
			continue
		}
		candidates, err := replacementCandidates(ctx, r.pool, id, item, issuedAt, expectedReturnAt)
		if err != nil {
			return rental.IssuePreview{}, err
		}
		conflicts = append(conflicts, rental.IssueConflict{Item: item, Replacements: candidates})
	}
	return rental.IssuePreview{Rental: value, IssuedAt: issuedAt, ExpectedReturnAt: expectedReturnAt, Conflicts: conflicts}, nil
}

// IssueMany одной транзакцией выдаёт все выбранные подтверждённые аренды.
// Если одна аренда или физическая единица недоступна, вся группа откатывается.
func (r *RentalRepository) IssueMany(
	ctx context.Context,
	actor user.User,
	ids []int64,
	issuedAt time.Time,
) ([]rental.Rental, error) {
	orderedIDs, err := validatedBulkRentalIDs(ids)
	if err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin issue rental selection transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	values := make([]rental.Rental, 0, len(orderedIDs))
	equipmentIDs := make([]int64, 0)
	seenEquipment := make(map[int64]struct{})
	for _, id := range orderedIDs {
		value, err := lockRental(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if value.Status != rental.StatusConfirmed {
			return nil, fmt.Errorf(
				"%w: %s -> %s", rental.ErrStatusTransitionNotAllowed, value.Status, rental.StatusActive,
			)
		}
		for _, item := range value.Items() {
			if _, exists := seenEquipment[item.EquipmentID]; exists {
				return nil, rental.ErrEquipmentUnavailable
			}
			seenEquipment[item.EquipmentID] = struct{}{}
			equipmentIDs = append(equipmentIDs, item.EquipmentID)
		}
		if err := value.Issue(issuedAt); err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	if err := lockAvailableEquipment(ctx, tx, equipmentIDs); err != nil {
		return nil, err
	}
	for _, value := range values {
		expectedReturnAt, _ := value.ExpectedReturnAt()
		conflicts, err := conflictingEquipmentIDs(
			ctx, tx, value.ID, issuedAt, expectedReturnAt, value.Items(),
		)
		if err != nil {
			return nil, err
		}
		if len(conflicts) > 0 {
			return nil, rental.ErrIssueConflict
		}
	}

	for _, value := range values {
		expectedReturnAt, ok := value.ExpectedReturnAt()
		if !ok {
			return nil, rental.ErrExpectedReturnAtRequired
		}
		result, err := tx.Exec(
			ctx,
			`UPDATE rentals
			 SET status = 'active', issued_at = $2, expected_return_at = $3
			 WHERE id = $1 AND status = 'confirmed'`,
			value.ID,
			issuedAt,
			expectedReturnAt,
		)
		if err != nil {
			return nil, fmt.Errorf("mark rental %d issued: %w", value.ID, err)
		}
		if result.RowsAffected() != 1 {
			return nil, rental.ErrStatusTransitionNotAllowed
		}
	}
	result, err := tx.Exec(
		ctx,
		"UPDATE equipment SET status = 'issued' WHERE id = ANY($1) AND status = 'available'",
		equipmentIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("mark selected rental equipment issued: %w", err)
	}
	if result.RowsAffected() != int64(len(equipmentIDs)) {
		return nil, rental.ErrEquipmentUnavailable
	}

	for _, value := range values {
		expectedReturnAt, _ := value.ExpectedReturnAt()
		if err := r.writeAudit(
			ctx, tx, actionRentalIssued, actor, value,
			rentalAuditDetails{
				ClientID: value.ClientID, PlannedStart: value.Interval.Start(), PlannedEnd: value.Interval.End(),
				EquipmentCount: value.ItemCount(), IssuedAt: &issuedAt,
				ExpectedReturnAt: &expectedReturnAt,
			},
		); err != nil {
			return nil, fmt.Errorf("write issued rental %d audit event: %w", value.ID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit issue rental selection transaction: %w", err)
	}
	return values, nil
}

// Cancel атомарно отменяет подтверждённую аренду и сохраняет обязательный
// audit event. Состав остаётся в истории, а его резервирование прекращается.
func (r *RentalRepository) Cancel(
	ctx context.Context,
	actor user.User,
	id int64,
) (rental.Rental, error) {
	values, err := r.CancelMany(ctx, actor, []int64{id})
	if err != nil {
		return rental.Rental{}, err
	}
	return values[0], nil
}

// CancelMany одной транзакцией отменяет все выбранные подтверждённые аренды.
// Состав и отдельный audit event каждой аренды сохраняются.
func (r *RentalRepository) CancelMany(
	ctx context.Context,
	actor user.User,
	ids []int64,
) ([]rental.Rental, error) {
	orderedIDs, err := validatedBulkRentalIDs(ids)
	if err != nil {
		return nil, err
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin cancel rental selection transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	values := make([]rental.Rental, 0, len(orderedIDs))
	for _, id := range orderedIDs {
		value, err := lockRental(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if value.Status != rental.StatusConfirmed {
			return nil, fmt.Errorf(
				"%w: %s -> %s", rental.ErrStatusTransitionNotAllowed, value.Status, rental.StatusCancelled,
			)
		}
		if err := value.Cancel(); err != nil {
			return nil, err
		}
		values = append(values, value)
	}

	for _, value := range values {
		result, err := tx.Exec(
			ctx,
			"UPDATE rentals SET status = 'cancelled' WHERE id = $1 AND status = 'confirmed'",
			value.ID,
		)
		if err != nil {
			return nil, fmt.Errorf("mark rental %d cancelled: %w", value.ID, err)
		}
		if result.RowsAffected() != 1 {
			return nil, rental.ErrStatusTransitionNotAllowed
		}
		if err := r.writeAudit(
			ctx, tx, actionRentalCancelled, actor, value,
			rentalAuditDetails{
				ClientID: value.ClientID, PlannedStart: value.Interval.Start(), PlannedEnd: value.Interval.End(),
				EquipmentCount: value.ItemCount(),
			},
		); err != nil {
			return nil, fmt.Errorf("write cancelled rental %d audit event: %w", value.ID, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit cancel rental selection transaction: %w", err)
	}
	return values, nil
}

func validatedBulkRentalIDs(ids []int64) ([]int64, error) {
	if len(ids) == 0 || len(ids) > rental.MaxBulkSelection {
		return nil, rental.ErrInvalidBulkSelection
	}
	ordered := append([]int64(nil), ids...)
	seen := make(map[int64]struct{}, len(ordered))
	for _, id := range ordered {
		if id <= 0 {
			return nil, rental.ErrInvalidBulkSelection
		}
		if _, exists := seen[id]; exists {
			return nil, rental.ErrInvalidBulkSelection
		}
		seen[id] = struct{}{}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	return ordered, nil
}

func lockRental(ctx context.Context, tx pgx.Tx, id int64) (rental.Rental, error) {
	const rentalQuery = `
		SELECT client_id, planned_start_at, planned_end_at, status, issued_at,
		       expected_return_at, returned_at,
		       overdue_slots, final_total_kopecks
		FROM rentals
		WHERE id = $1
		FOR UPDATE
	`
	var (
		clientID         int64
		plannedStart     time.Time
		plannedEnd       time.Time
		status           rental.Status
		issuedAt         *time.Time
		expectedReturnAt *time.Time
		returnedAt       *time.Time
		overdueSlots     *int
		finalTotal       *int64
	)
	if err := tx.QueryRow(ctx, rentalQuery, id).Scan(
		&clientID, &plannedStart, &plannedEnd, &status, &issuedAt, &expectedReturnAt, &returnedAt,
		&overdueSlots, &finalTotal,
	); errors.Is(err, pgx.ErrNoRows) {
		return rental.Rental{}, rental.ErrRentalNotFound
	} else if err != nil {
		return rental.Rental{}, fmt.Errorf("lock rental %d: %w", id, err)
	}

	const itemsQuery = `
		SELECT equipment_id, inventory_number, kind, model_code, hourly_rate_kopecks
		FROM rental_items
		WHERE rental_id = $1
		ORDER BY position
	`
	rows, err := tx.Query(ctx, itemsQuery, id)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("query rental %d items: %w", id, err)
	}
	defer rows.Close()
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
			return rental.Rental{}, fmt.Errorf("scan rental %d item: %w", id, err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return rental.Rental{}, fmt.Errorf("iterate rental %d items: %w", id, err)
	}

	interval, err := rental.NewInterval(plannedStart, plannedEnd)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("restore rental %d interval: %w", id, err)
	}
	value, err := rental.RestoreWithExpectedReturn(
		id, clientID, interval, status, issuedAt, expectedReturnAt, returnedAt, items,
	)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("restore rental %d: %w", id, err)
	}
	if err := restoreRentalSettlement(&value, overdueSlots, finalTotal); err != nil {
		return rental.Rental{}, fmt.Errorf("restore rental %d settlement: %w", id, err)
	}
	return value, nil
}

func lockAvailableEquipment(ctx context.Context, tx pgx.Tx, ids []int64) error {
	ordered := append([]int64(nil), ids...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	rows, err := tx.Query(
		ctx,
		"SELECT id, status FROM equipment WHERE id = ANY($1) ORDER BY id FOR UPDATE",
		ordered,
	)
	if err != nil {
		return fmt.Errorf("lock selected rental equipment: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var status equipment.Status
		if err := rows.Scan(&id, &status); err != nil {
			return fmt.Errorf("scan selected rental equipment: %w", err)
		}
		if status != equipment.StatusAvailable {
			return rental.ErrEquipmentUnavailable
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate selected rental equipment: %w", err)
	}
	if count != len(ordered) {
		return rental.ErrEquipmentUnavailable
	}
	return nil
}

type rentalQueryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func lockEquipmentForIssue(ctx context.Context, tx pgx.Tx, ids []int64) error {
	unique := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return rental.ErrInvalidReplacement
		}
		unique[id] = struct{}{}
	}
	ordered := make([]int64, 0, len(unique))
	for id := range unique {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	rows, err := tx.Query(ctx, "SELECT id FROM equipment WHERE id = ANY($1) ORDER BY id FOR UPDATE", ordered)
	if err != nil {
		return fmt.Errorf("lock equipment for rental issue: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate equipment for rental issue: %w", err)
	}
	if count != len(ordered) {
		return rental.ErrEquipmentUnavailable
	}
	return nil
}

func conflictingEquipmentIDs(
	ctx context.Context,
	queryer rentalQueryer,
	rentalID int64,
	issuedAt, expectedReturnAt time.Time,
	items []rental.Item,
) (map[int64]struct{}, error) {
	conflicts := make(map[int64]struct{})
	const query = `
		SELECT current.status <> 'available' OR EXISTS (
			SELECT 1
			FROM rental_items AS other_item
			JOIN rentals AS other ON other.id = other_item.rental_id
			WHERE other_item.equipment_id = $1
			  AND other.id <> $2
			  AND (
				(other.status = 'confirmed'
				 AND other.planned_start_at < $4 AND $3 < other.planned_end_at)
				OR
				(other.status = 'active'
				 AND other.issued_at < $4 AND $3 < other.expected_return_at)
			  )
		)
		FROM equipment AS current
		WHERE current.id = $1
	`
	for _, item := range items {
		var conflict bool
		if err := queryer.QueryRow(
			ctx, query, item.EquipmentID, rentalID, issuedAt, expectedReturnAt,
		).Scan(&conflict); err != nil {
			return nil, fmt.Errorf("check equipment %d issue conflict: %w", item.EquipmentID, err)
		}
		if conflict {
			conflicts[item.EquipmentID] = struct{}{}
		}
	}
	return conflicts, nil
}

func replacementCandidates(
	ctx context.Context,
	queryer rentalQueryer,
	rentalID int64,
	original rental.Item,
	issuedAt, expectedReturnAt time.Time,
) ([]equipment.Item, error) {
	const query = `
		SELECT candidate.id, candidate.model_id, candidate.sequence_number,
		       model.kind, model.model_code, model.hourly_rate_kopecks, candidate.status
		FROM equipment AS source
		JOIN equipment AS candidate ON candidate.model_id = source.model_id
		JOIN equipment_models AS model ON model.id = candidate.model_id
		WHERE source.id = $1
		  AND candidate.id <> source.id
		  AND candidate.status = 'available'
		  AND NOT EXISTS (
			SELECT 1 FROM rental_items current_item
			WHERE current_item.rental_id = $2 AND current_item.equipment_id = candidate.id
		  )
		  AND NOT EXISTS (
			SELECT 1
			FROM rental_items other_item
			JOIN rentals other ON other.id = other_item.rental_id
			WHERE other_item.equipment_id = candidate.id
			  AND other.id <> $2
			  AND (
				(other.status = 'confirmed'
				 AND other.planned_start_at < $4 AND $3 < other.planned_end_at)
				OR
				(other.status = 'active'
				 AND other.issued_at < $4 AND $3 < other.expected_return_at)
			  )
		  )
		ORDER BY candidate.id
	`
	rows, err := queryer.Query(ctx, query, original.EquipmentID, rentalID, issuedAt, expectedReturnAt)
	if err != nil {
		return nil, fmt.Errorf("query equipment replacement candidates: %w", err)
	}
	defer rows.Close()
	items := make([]equipment.Item, 0)
	for rows.Next() {
		var item equipment.Item
		if err := rows.Scan(
			&item.ID, &item.ModelID, &item.SequenceNumber, &item.Kind, &item.ModelCode,
			&item.HourlyRateKopecks, &item.Status,
		); err != nil {
			return nil, fmt.Errorf("scan equipment replacement candidate: %w", err)
		}
		item.InventoryNumber, err = equipment.InventoryNumber(item.Kind, item.ModelCode, item.SequenceNumber)
		if err != nil {
			return nil, fmt.Errorf("build equipment replacement inventory number: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate equipment replacement candidates: %w", err)
	}
	return items, nil
}

func loadReplacementCandidate(
	ctx context.Context,
	queryer rentalQueryer,
	rentalID int64,
	original rental.Item,
	replacementID int64,
	issuedAt, expectedReturnAt time.Time,
) (equipment.Item, error) {
	candidates, err := replacementCandidates(ctx, queryer, rentalID, original, issuedAt, expectedReturnAt)
	if err != nil {
		return equipment.Item{}, err
	}
	for _, candidate := range candidates {
		if candidate.ID == replacementID {
			return candidate, nil
		}
	}
	return equipment.Item{}, rental.ErrInvalidReplacement
}

func markRentalIssued(
	ctx context.Context,
	tx pgx.Tx,
	value rental.Rental,
	issuedAt time.Time,
	equipmentIDs []int64,
) error {
	expectedReturnAt, ok := value.ExpectedReturnAt()
	if !ok {
		return rental.ErrExpectedReturnAtRequired
	}
	result, err := tx.Exec(ctx,
		`UPDATE rentals SET status = 'active', issued_at = $2, expected_return_at = $3
		 WHERE id = $1 AND status = 'confirmed'`,
		value.ID, issuedAt, expectedReturnAt,
	)
	if err != nil {
		return fmt.Errorf("mark rental %d issued: %w", value.ID, err)
	}
	if result.RowsAffected() != 1 {
		return rental.ErrStatusTransitionNotAllowed
	}
	result, err = tx.Exec(ctx,
		"UPDATE equipment SET status = 'issued' WHERE id = ANY($1) AND status = 'available'",
		equipmentIDs,
	)
	if err != nil {
		return fmt.Errorf("mark rental equipment issued: %w", err)
	}
	if result.RowsAffected() != int64(len(equipmentIDs)) {
		return rental.ErrEquipmentUnavailable
	}
	return nil
}

func lockIssuedEquipment(ctx context.Context, tx pgx.Tx, ids []int64) error {
	ordered := append([]int64(nil), ids...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	rows, err := tx.Query(
		ctx,
		"SELECT id, status FROM equipment WHERE id = ANY($1) ORDER BY id FOR UPDATE",
		ordered,
	)
	if err != nil {
		return fmt.Errorf("lock returned rental equipment: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var status equipment.Status
		if err := rows.Scan(&id, &status); err != nil {
			return fmt.Errorf("scan returned rental equipment: %w", err)
		}
		if status != equipment.StatusIssued {
			return rental.ErrEquipmentUnavailable
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate returned rental equipment: %w", err)
	}
	if count != len(ordered) {
		return rental.ErrEquipmentUnavailable
	}
	return nil
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
		id, value.ClientID, value.Interval, value.Status, nil, nil, value.Items(),
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
		       r.issued_at, r.expected_return_at, r.returned_at,
		       r.overdue_slots, r.final_total_kopecks,
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
		found            bool
		clientID         int64
		start            time.Time
		end              time.Time
		status           rental.Status
		issuedAt         *time.Time
		expectedReturnAt *time.Time
		returnedAt       *time.Time
		overdueSlots     *int
		finalTotal       *int64
		items            []rental.Item
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
			&expectedReturnAt,
			&returnedAt,
			&overdueSlots,
			&finalTotal,
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
	restored, err := rental.RestoreWithExpectedReturn(
		id, clientID, interval, status, issuedAt, expectedReturnAt, returnedAt, items,
	)
	if err != nil {
		return rental.Rental{}, fmt.Errorf("restore rental: %w", err)
	}
	if err := restoreRentalSettlement(&restored, overdueSlots, finalTotal); err != nil {
		return rental.Rental{}, fmt.Errorf("restore rental settlement: %w", err)
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
		       r.issued_at, r.expected_return_at, r.returned_at,
		       count(ri.equipment_id),
		       COALESCE(sum(ri.hourly_rate_kopecks), 0), r.final_total_kopecks
		FROM rentals AS r
		JOIN clients AS c ON c.id = r.client_id
		LEFT JOIN rental_items AS ri ON ri.rental_id = r.id
		WHERE r.status = ANY($3::text[])
		GROUP BY r.id, c.full_name
		ORDER BY (r.status = 'confirmed' AND r.planned_start_at <= CURRENT_TIMESTAMP) DESC,
		         r.id DESC
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
			finalTotal    *int64
		)
		if err := rows.Scan(
			&summary.ID,
			&summary.ClientID,
			&summary.ClientName,
			&start,
			&end,
			&summary.Status,
			&summary.IssuedAt,
			&summary.ExpectedReturnAt,
			&summary.ReturnedAt,
			&summary.ItemCount,
			&hourlyRateSum,
			&finalTotal,
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
		summary.FinalTotalKopecks = finalTotal
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

func restoreRentalSettlement(value *rental.Rental, overdueSlots *int, finalTotal *int64) error {
	if value.Status == rental.StatusCompleted {
		if overdueSlots == nil || finalTotal == nil {
			return rental.ErrInvalidSettlement
		}
		return value.RestoreSettlement(*overdueSlots, *finalTotal)
	}
	if overdueSlots != nil || finalTotal != nil {
		return rental.ErrInvalidSettlement
	}
	return nil
}

// AvailableEquipment возвращает физические единицы, прогнозно доступные на весь
// переданный полуоткрытый интервал. Подтверждённая аренда занимает только свой
// плановый период, а активная — фактический период до ожидаемого возврата.
func (r *RentalRepository) AvailableEquipment(
	ctx context.Context,
	interval rental.Interval,
) ([]equipment.Item, error) {
	const query = `
		SELECT e.id, e.model_id, e.sequence_number,
		       m.kind, m.model_code, m.hourly_rate_kopecks, e.status
		FROM equipment AS e
		JOIN equipment_models AS m ON m.id = e.model_id
		WHERE e.status IN ('available', 'issued')
		  AND NOT EXISTS (
		      SELECT 1
		      FROM rental_items AS ri
		      JOIN rentals AS r ON r.id = ri.rental_id
		      WHERE ri.equipment_id = e.id
		        AND (
		            (r.status = 'confirmed'
		             AND r.planned_start_at < $2 AND $1 < r.planned_end_at)
		            OR
		            (r.status = 'active'
		             AND r.issued_at < $2 AND $1 < r.expected_return_at)
		        )
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
		  AND e.status IN ('available', 'issued')
		  AND NOT EXISTS (
		      SELECT 1
		      FROM rental_items AS ri
		      JOIN rentals AS r ON r.id = ri.rental_id
		      WHERE ri.equipment_id = e.id
		        AND (
		            (r.status = 'confirmed'
		             AND r.planned_start_at < $3 AND $2 < r.planned_end_at)
		            OR
		            (r.status = 'active'
		             AND r.issued_at < $3 AND $2 < r.expected_return_at)
		        )
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
