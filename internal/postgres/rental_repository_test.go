package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
	"github.com/Dyuzhovsergey/sup-rental/internal/rental"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRentalRepositoryCreatesConfirmedRentalWithSnapshotsAndAudit(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 3)
	repository := NewRentalRepository(pool)
	interval := rentalTestInterval(t, time.Date(2026, 9, 1, 10, 8, 0, 0, time.UTC))

	created, err := repository.CreateConfirmed(
		ctx, fixture.actor, fixture.firstClientID, interval,
		[]rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 2}},
	)
	if err != nil {
		t.Fatalf("CreateConfirmed() error = %v; apply migrations to TEST_DATABASE_URL first", err)
	}
	if created.ID <= 0 || created.Status != rental.StatusConfirmed || created.ItemCount() != 2 {
		t.Fatalf("created = %+v items = %+v", created, created.Items())
	}
	got, err := repository.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertRentalEqual(t, got, created)

	if _, err := pool.Exec(ctx, "UPDATE equipment_models SET hourly_rate_kopecks = 60000 WHERE id = $1", fixture.modelID); err != nil {
		t.Fatalf("change current rate: %v", err)
	}
	stored, err := repository.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() after rate change error = %v", err)
	}
	for _, item := range stored.Items() {
		if item.HourlyRateKopecks != 50_000 {
			t.Errorf("snapshot rate = %d, want 50000", item.HourlyRateKopecks)
		}
	}

	var action, actorLogin, actorRole, targetType, targetLabel string
	var details string
	if err := pool.QueryRow(ctx, `SELECT action, actor_login, actor_role, target_type, target_label, details::text
		FROM audit_events WHERE action = 'rental.confirmed' AND target_id = $1`, created.ID).Scan(
		&action, &actorLogin, &actorRole, &targetType, &targetLabel, &details,
	); err != nil {
		t.Fatalf("query rental audit: %v", err)
	}
	if action != "rental.confirmed" || actorLogin != fixture.actor.Login || actorRole != "operator" ||
		targetType != "rental" || targetLabel != fmt.Sprintf("Аренда №%d", created.ID) ||
		!containsAll(details, `"client_id":`, `"equipment_count": 2`) &&
			!containsAll(details, `"client_id":`, `"equipment_count":2`) {
		t.Errorf("audit = %q %q %q %q %q %s", action, actorLogin, actorRole, targetType, targetLabel, details)
	}
}

func TestRentalRepositoryIssuesRentalWithEquipmentAndAudit(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 2)
	repository := NewRentalRepository(pool)
	interval := rentalTestInterval(t, time.Date(2026, 9, 1, 12, 8, 0, 0, time.UTC))
	created, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.firstClientID, interval,
		[]rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 2}})
	if err != nil {
		t.Fatalf("CreateConfirmed() error = %v", err)
	}
	issuedAt := time.Date(2026, 9, 1, 11, 57, 0, 0, time.UTC)
	issued, err := repository.Issue(ctx, fixture.actor, created.ID, issuedAt)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if issued.Status != rental.StatusActive {
		t.Fatalf("Issue() status = %q", issued.Status)
	}
	gotIssuedAt, ok := issued.IssuedAt()
	if !ok || !gotIssuedAt.Equal(issuedAt) {
		t.Fatalf("Issue() issuedAt = %v, %v", gotIssuedAt, ok)
	}
	stored, err := repository.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertRentalEqual(t, stored, issued)

	var issuedEquipment int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM equipment
		WHERE id = ANY($1) AND status = 'issued'`, fixture.equipmentIDs).Scan(&issuedEquipment); err != nil {
		t.Fatalf("count issued equipment: %v", err)
	}
	if issuedEquipment != 2 {
		t.Fatalf("issued equipment = %d", issuedEquipment)
	}
	var details string
	if err := pool.QueryRow(ctx, `SELECT details::text FROM audit_events
		WHERE action = 'rental.issued' AND target_id = $1`, created.ID).Scan(&details); err != nil {
		t.Fatalf("query issued audit: %v", err)
	}
	if !containsAll(details, `"equipment_count": 2`, `"issued_at":`) &&
		!containsAll(details, `"equipment_count":2`, `"issued_at":`) {
		t.Fatalf("issued audit details = %s", details)
	}
}

func TestRentalRepositoryCompletesRentalAndReturnsEquipment(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 2)
	repository := NewRentalRepository(pool)
	created, err := repository.CreateConfirmed(
		ctx, fixture.actor, fixture.firstClientID,
		rentalTestInterval(t, time.Date(2026, 9, 1, 12, 8, 0, 0, time.UTC)),
		[]rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 2}},
	)
	if err != nil {
		t.Fatalf("CreateConfirmed() error = %v", err)
	}
	issuedAt := time.Date(2026, 9, 1, 11, 57, 0, 0, time.UTC)
	if _, err := repository.Issue(ctx, fixture.actor, created.ID, issuedAt); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	returnedAt := time.Date(2026, 9, 1, 13, 14, 0, 0, time.UTC)
	completed, err := repository.Complete(ctx, fixture.actor, created.ID, returnedAt)
	if err != nil {
		t.Fatalf("Complete() error = %v; apply migration 014 to TEST_DATABASE_URL first", err)
	}
	if completed.Status != rental.StatusCompleted {
		t.Fatalf("Complete() status = %q", completed.Status)
	}
	gotReturnedAt, ok := completed.ReturnedAt()
	if !ok || !gotReturnedAt.Equal(returnedAt) {
		t.Fatalf("Complete() returnedAt = %v, %v", gotReturnedAt, ok)
	}
	stored, err := repository.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertRentalEqual(t, stored, completed)

	var availableEquipment int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM equipment
		WHERE id = ANY($1) AND status = 'available'`, fixture.equipmentIDs).Scan(&availableEquipment); err != nil {
		t.Fatalf("count available equipment: %v", err)
	}
	if availableEquipment != 2 {
		t.Fatalf("available equipment = %d", availableEquipment)
	}
	var details string
	if err := pool.QueryRow(ctx, `SELECT details::text FROM audit_events
		WHERE action = 'rental.completed' AND target_id = $1`, created.ID).Scan(&details); err != nil {
		t.Fatalf("query completed audit: %v", err)
	}
	if !containsAll(details, `"equipment_count": 2`, `"issued_at":`, `"returned_at":`) &&
		!containsAll(details, `"equipment_count":2`, `"issued_at":`, `"returned_at":`) {
		t.Fatalf("completed audit details = %s", details)
	}
}

func TestRentalRepositoryCompletionIsConcurrentAndTransactional(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	repository := NewRentalRepository(pool)
	created, err := repository.CreateConfirmed(
		ctx, fixture.actor, fixture.firstClientID,
		rentalTestInterval(t, time.Date(2026, 9, 1, 14, 8, 0, 0, time.UTC)),
		[]rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}},
	)
	if err != nil {
		t.Fatalf("CreateConfirmed() error = %v", err)
	}
	issuedAt := time.Date(2026, 9, 1, 13, 58, 0, 0, time.UTC)
	if _, err := repository.Issue(ctx, fixture.actor, created.ID, issuedAt); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, completeErr := repository.Complete(ctx, fixture.actor, created.ID, issuedAt.Add(time.Hour))
			errorsCh <- completeErr
		}()
	}
	close(start)
	wait.Wait()
	close(errorsCh)
	successes, conflicts := 0, 0
	for completeErr := range errorsCh {
		switch {
		case completeErr == nil:
			successes++
		case errors.Is(completeErr, rental.ErrStatusTransitionNotAllowed):
			conflicts++
		default:
			t.Fatalf("concurrent Complete() error = %v", completeErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes = %d conflicts = %d", successes, conflicts)
	}

	rollbackFixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	rollbackRepository := NewRentalRepository(pool)
	rollbackRental, err := rollbackRepository.CreateConfirmed(
		ctx, rollbackFixture.actor, rollbackFixture.firstClientID,
		rentalTestInterval(t, time.Date(2026, 9, 1, 16, 8, 0, 0, time.UTC)),
		[]rental.ModelSelection{{ModelID: rollbackFixture.modelID, Quantity: 1}},
	)
	if err != nil {
		t.Fatalf("rollback CreateConfirmed() error = %v", err)
	}
	if _, err := rollbackRepository.Issue(ctx, rollbackFixture.actor, rollbackRental.ID, issuedAt); err != nil {
		t.Fatalf("rollback Issue() error = %v", err)
	}
	rollbackRepository.writeAudit = func(context.Context, pgx.Tx, string, user.User, rental.Rental, rentalAuditDetails) error {
		return errors.New("audit unavailable")
	}
	if _, err := rollbackRepository.Complete(ctx, rollbackFixture.actor, rollbackRental.ID, issuedAt.Add(time.Hour)); err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("Complete() audit error = %v", err)
	}
	var rentalStatus rental.Status
	var returnedAt *time.Time
	var equipmentStatus equipment.Status
	if err := pool.QueryRow(ctx, "SELECT status, returned_at FROM rentals WHERE id = $1", rollbackRental.ID).Scan(&rentalStatus, &returnedAt); err != nil {
		t.Fatalf("query rolled back rental: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT status FROM equipment WHERE id = $1", rollbackFixture.equipmentIDs[0]).Scan(&equipmentStatus); err != nil {
		t.Fatalf("query rolled back equipment: %v", err)
	}
	if rentalStatus != rental.StatusActive || returnedAt != nil || equipmentStatus != equipment.StatusIssued {
		t.Fatalf("rollback state = %q, returned %v, equipment %q", rentalStatus, returnedAt, equipmentStatus)
	}
}

func TestRentalRepositoryRejectsCompletionWithNonIssuedEquipment(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	repository := NewRentalRepository(pool)
	created, err := repository.CreateConfirmed(
		ctx, fixture.actor, fixture.firstClientID,
		rentalTestInterval(t, time.Date(2026, 9, 1, 18, 8, 0, 0, time.UTC)),
		[]rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}},
	)
	if err != nil {
		t.Fatalf("CreateConfirmed() error = %v", err)
	}
	issuedAt := time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)
	if _, err := repository.Issue(ctx, fixture.actor, created.ID, issuedAt); err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE equipment SET status = 'maintenance' WHERE id = $1", fixture.equipmentIDs[0]); err != nil {
		t.Fatalf("mark equipment maintenance: %v", err)
	}
	if _, err := repository.Complete(ctx, fixture.actor, created.ID, issuedAt.Add(time.Hour)); !errors.Is(err, rental.ErrEquipmentUnavailable) {
		t.Fatalf("Complete() error = %v, want ErrEquipmentUnavailable", err)
	}
	var status rental.Status
	var returnedAt *time.Time
	if err := pool.QueryRow(ctx, "SELECT status, returned_at FROM rentals WHERE id = $1", created.ID).Scan(&status, &returnedAt); err != nil {
		t.Fatalf("query rental after conflict: %v", err)
	}
	var auditCount int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action = 'rental.completed' AND target_id = $1", created.ID).Scan(&auditCount); err != nil {
		t.Fatalf("count completed audit: %v", err)
	}
	if status != rental.StatusActive || returnedAt != nil || auditCount != 0 {
		t.Fatalf("state after conflict = %q, returned %v, audit %d", status, returnedAt, auditCount)
	}
}

func TestRentalRepositoryIssuesSelectedRentalsAtomically(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 2)
	repository := NewRentalRepository(pool)
	interval := rentalTestInterval(t, time.Date(2026, 9, 5, 10, 8, 0, 0, time.UTC))
	selection := []rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}}
	first, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.firstClientID, interval, selection)
	if err != nil {
		t.Fatalf("first CreateConfirmed() error = %v", err)
	}
	second, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.secondClientID, interval, selection)
	if err != nil {
		t.Fatalf("second CreateConfirmed() error = %v", err)
	}
	issuedAt := time.Date(2026, 9, 5, 9, 55, 0, 0, time.UTC)
	issued, err := repository.IssueMany(ctx, fixture.actor, []int64{second.ID, first.ID}, issuedAt)
	if err != nil {
		t.Fatalf("IssueMany() error = %v", err)
	}
	if len(issued) != 2 || issued[0].ID != first.ID || issued[1].ID != second.ID {
		t.Fatalf("IssueMany() = %+v", issued)
	}
	for _, value := range issued {
		gotIssuedAt, ok := value.IssuedAt()
		if value.Status != rental.StatusActive || !ok || !gotIssuedAt.Equal(issuedAt) {
			t.Errorf("issued rental = %+v issuedAt = %v, %v", value, gotIssuedAt, ok)
		}
	}
	var activeRentals, issuedEquipment, auditEvents int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM rentals WHERE id = ANY($1) AND status = 'active'", []int64{first.ID, second.ID}).Scan(&activeRentals); err != nil {
		t.Fatalf("count active rentals: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM equipment WHERE id = ANY($1) AND status = 'issued'", fixture.equipmentIDs).Scan(&issuedEquipment); err != nil {
		t.Fatalf("count issued equipment: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action = 'rental.issued' AND target_id = ANY($1)", []int64{first.ID, second.ID}).Scan(&auditEvents); err != nil {
		t.Fatalf("count issued audit events: %v", err)
	}
	if activeRentals != 2 || issuedEquipment != 2 || auditEvents != 2 {
		t.Fatalf("active = %d issued equipment = %d audits = %d", activeRentals, issuedEquipment, auditEvents)
	}
}

func TestRentalRepositoryCompletesSelectedRentalsAtomically(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 2)
	repository := NewRentalRepository(pool)
	interval := rentalTestInterval(t, time.Date(2026, 9, 5, 11, 8, 0, 0, time.UTC))
	selection := []rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}}
	first, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.firstClientID, interval, selection)
	if err != nil {
		t.Fatalf("first CreateConfirmed() error = %v", err)
	}
	second, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.secondClientID, interval, selection)
	if err != nil {
		t.Fatalf("second CreateConfirmed() error = %v", err)
	}
	issuedAt := time.Date(2026, 9, 5, 10, 55, 0, 0, time.UTC)
	if _, err := repository.Issue(ctx, fixture.actor, first.ID, issuedAt); err != nil {
		t.Fatalf("Issue(first) error = %v", err)
	}

	returnedAt := time.Date(2026, 9, 5, 13, 12, 0, 0, time.UTC)
	if _, err := repository.CompleteMany(ctx, fixture.actor, []int64{first.ID, second.ID}, returnedAt); !errors.Is(err, rental.ErrStatusTransitionNotAllowed) {
		t.Fatalf("CompleteMany() mixed statuses error = %v", err)
	}
	var firstStatus rental.Status
	var firstReturnedAt *time.Time
	if err := pool.QueryRow(ctx, "SELECT status, returned_at FROM rentals WHERE id = $1", first.ID).Scan(&firstStatus, &firstReturnedAt); err != nil {
		t.Fatalf("query first rental after rollback: %v", err)
	}
	if firstStatus != rental.StatusActive || firstReturnedAt != nil {
		t.Fatalf("first rental after rollback = %q returnedAt %v", firstStatus, firstReturnedAt)
	}

	if _, err := repository.Issue(ctx, fixture.actor, second.ID, issuedAt); err != nil {
		t.Fatalf("Issue(second) error = %v", err)
	}
	completed, err := repository.CompleteMany(ctx, fixture.actor, []int64{second.ID, first.ID}, returnedAt)
	if err != nil {
		t.Fatalf("CompleteMany() error = %v", err)
	}
	if len(completed) != 2 || completed[0].ID != first.ID || completed[1].ID != second.ID {
		t.Fatalf("CompleteMany() = %+v", completed)
	}
	for _, value := range completed {
		gotReturnedAt, ok := value.ReturnedAt()
		if value.Status != rental.StatusCompleted || !ok || !gotReturnedAt.Equal(returnedAt) {
			t.Errorf("completed rental = %+v returnedAt = %v, %v", value, gotReturnedAt, ok)
		}
	}
	var completedRentals, availableEquipment, auditEvents int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM rentals WHERE id = ANY($1) AND status = 'completed'", []int64{first.ID, second.ID}).Scan(&completedRentals); err != nil {
		t.Fatalf("count completed rentals: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM equipment WHERE id = ANY($1) AND status = 'available'", fixture.equipmentIDs).Scan(&availableEquipment); err != nil {
		t.Fatalf("count available equipment: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action = 'rental.completed' AND target_id = ANY($1)", []int64{first.ID, second.ID}).Scan(&auditEvents); err != nil {
		t.Fatalf("count completed audit events: %v", err)
	}
	if completedRentals != 2 || availableEquipment != 2 || auditEvents != 2 {
		t.Fatalf("completed = %d available equipment = %d audits = %d", completedRentals, availableEquipment, auditEvents)
	}

	rollbackFixture := newRentalRepositoryFixture(t, ctx, pool, 2)
	rollbackRepository := NewRentalRepository(pool)
	rollbackInterval := rentalTestInterval(t, time.Date(2026, 9, 5, 15, 8, 0, 0, time.UTC))
	rollbackFirst, err := rollbackRepository.CreateConfirmed(
		ctx, rollbackFixture.actor, rollbackFixture.firstClientID, rollbackInterval,
		[]rental.ModelSelection{{ModelID: rollbackFixture.modelID, Quantity: 1}},
	)
	if err != nil {
		t.Fatalf("rollback first CreateConfirmed() error = %v", err)
	}
	rollbackSecond, err := rollbackRepository.CreateConfirmed(
		ctx, rollbackFixture.actor, rollbackFixture.secondClientID, rollbackInterval,
		[]rental.ModelSelection{{ModelID: rollbackFixture.modelID, Quantity: 1}},
	)
	if err != nil {
		t.Fatalf("rollback second CreateConfirmed() error = %v", err)
	}
	if _, err := rollbackRepository.IssueMany(
		ctx, rollbackFixture.actor, []int64{rollbackFirst.ID, rollbackSecond.ID}, issuedAt,
	); err != nil {
		t.Fatalf("rollback IssueMany() error = %v", err)
	}
	auditCalls := 0
	rollbackRepository.writeAudit = func(context.Context, pgx.Tx, string, user.User, rental.Rental, rentalAuditDetails) error {
		auditCalls++
		if auditCalls == 2 {
			return errors.New("audit unavailable")
		}
		return nil
	}
	if _, err := rollbackRepository.CompleteMany(
		ctx, rollbackFixture.actor, []int64{rollbackFirst.ID, rollbackSecond.ID}, returnedAt,
	); err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("CompleteMany() audit error = %v", err)
	}
	if err := pool.QueryRow(
		ctx, "SELECT count(*) FROM rentals WHERE id = ANY($1) AND status = 'active' AND returned_at IS NULL",
		[]int64{rollbackFirst.ID, rollbackSecond.ID},
	).Scan(&completedRentals); err != nil {
		t.Fatalf("count active rentals after audit rollback: %v", err)
	}
	if err := pool.QueryRow(
		ctx, "SELECT count(*) FROM equipment WHERE id = ANY($1) AND status = 'issued'", rollbackFixture.equipmentIDs,
	).Scan(&availableEquipment); err != nil {
		t.Fatalf("count issued equipment after audit rollback: %v", err)
	}
	if completedRentals != 2 || availableEquipment != 2 {
		t.Fatalf("after audit rollback active = %d issued equipment = %d", completedRentals, availableEquipment)
	}
}

func TestRentalRepositoryCancelsSelectedRentalsAtomically(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 2)
	repository := NewRentalRepository(pool)
	interval := rentalTestInterval(t, time.Date(2026, 9, 5, 12, 8, 0, 0, time.UTC))
	selection := []rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}}
	first, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.firstClientID, interval, selection)
	if err != nil {
		t.Fatalf("first CreateConfirmed() error = %v", err)
	}
	second, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.secondClientID, interval, selection)
	if err != nil {
		t.Fatalf("second CreateConfirmed() error = %v", err)
	}
	cancelled, err := repository.CancelMany(ctx, fixture.actor, []int64{second.ID, first.ID})
	if err != nil {
		t.Fatalf("CancelMany() error = %v", err)
	}
	if len(cancelled) != 2 || cancelled[0].ID != first.ID || cancelled[1].ID != second.ID {
		t.Fatalf("CancelMany() = %+v", cancelled)
	}
	var cancelledRentals, availableEquipment, auditEvents int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM rentals WHERE id = ANY($1) AND status = 'cancelled'", []int64{first.ID, second.ID}).Scan(&cancelledRentals); err != nil {
		t.Fatalf("count cancelled rentals: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM equipment WHERE id = ANY($1) AND status = 'available'", fixture.equipmentIDs).Scan(&availableEquipment); err != nil {
		t.Fatalf("count available equipment: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action = 'rental.cancelled' AND target_id = ANY($1)", []int64{first.ID, second.ID}).Scan(&auditEvents); err != nil {
		t.Fatalf("count cancelled audit events: %v", err)
	}
	if cancelledRentals != 2 || availableEquipment != 2 || auditEvents != 2 {
		t.Fatalf("cancelled = %d available equipment = %d audits = %d", cancelledRentals, availableEquipment, auditEvents)
	}
}

func TestRentalRepositoryBulkActionsRejectConflictsAndRollBack(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	repository := NewRentalRepository(pool)
	selection := []rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}}
	first, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.firstClientID,
		rentalTestInterval(t, time.Date(2026, 9, 5, 14, 8, 0, 0, time.UTC)), selection)
	if err != nil {
		t.Fatalf("first CreateConfirmed() error = %v", err)
	}
	second, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.secondClientID,
		rentalTestInterval(t, time.Date(2026, 9, 5, 16, 8, 0, 0, time.UTC)), selection)
	if err != nil {
		t.Fatalf("second CreateConfirmed() error = %v", err)
	}
	if _, err := repository.IssueMany(ctx, fixture.actor, []int64{first.ID, second.ID}, time.Now().UTC()); !errors.Is(err, rental.ErrEquipmentUnavailable) {
		t.Fatalf("IssueMany() duplicate equipment error = %v", err)
	}
	var confirmedRentals int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM rentals WHERE id = ANY($1) AND status = 'confirmed'", []int64{first.ID, second.ID}).Scan(&confirmedRentals); err != nil {
		t.Fatalf("count confirmed rentals: %v", err)
	}
	if confirmedRentals != 2 {
		t.Fatalf("confirmed rentals after conflict = %d", confirmedRentals)
	}

	rollbackFixture := newRentalRepositoryFixture(t, ctx, pool, 2)
	rollbackRepository := NewRentalRepository(pool)
	interval := rentalTestInterval(t, time.Date(2026, 9, 5, 18, 8, 0, 0, time.UTC))
	rollbackSelection := []rental.ModelSelection{{ModelID: rollbackFixture.modelID, Quantity: 1}}
	rollbackFirst, err := rollbackRepository.CreateConfirmed(ctx, rollbackFixture.actor, rollbackFixture.firstClientID, interval, rollbackSelection)
	if err != nil {
		t.Fatalf("rollback first CreateConfirmed() error = %v", err)
	}
	rollbackSecond, err := rollbackRepository.CreateConfirmed(ctx, rollbackFixture.actor, rollbackFixture.secondClientID, interval, rollbackSelection)
	if err != nil {
		t.Fatalf("rollback second CreateConfirmed() error = %v", err)
	}
	auditCalls := 0
	rollbackRepository.writeAudit = func(context.Context, pgx.Tx, string, user.User, rental.Rental, rentalAuditDetails) error {
		auditCalls++
		if auditCalls == 2 {
			return errors.New("audit unavailable")
		}
		return nil
	}
	if _, err := rollbackRepository.CancelMany(ctx, rollbackFixture.actor, []int64{rollbackFirst.ID, rollbackSecond.ID}); err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("CancelMany() audit error = %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM rentals WHERE id = ANY($1) AND status = 'confirmed'", []int64{rollbackFirst.ID, rollbackSecond.ID}).Scan(&confirmedRentals); err != nil {
		t.Fatalf("count rolled back rentals: %v", err)
	}
	if confirmedRentals != 2 {
		t.Fatalf("confirmed rentals after audit rollback = %d", confirmedRentals)
	}
}

func TestValidatedBulkRentalIDs(t *testing.T) {
	tooMany := make([]int64, rental.MaxBulkSelection+1)
	for index := range tooMany {
		tooMany[index] = int64(index + 1)
	}
	for _, ids := range [][]int64{nil, {}, {0}, {-1}, {1, 1}, tooMany} {
		if _, err := validatedBulkRentalIDs(ids); !errors.Is(err, rental.ErrInvalidBulkSelection) {
			t.Errorf("validatedBulkRentalIDs(%v) error = %v", ids, err)
		}
	}
	got, err := validatedBulkRentalIDs([]int64{3, 1, 2})
	if err != nil || fmt.Sprint(got) != "[1 2 3]" {
		t.Fatalf("validatedBulkRentalIDs() = %v, %v", got, err)
	}
}

func TestRentalRepositoryCancelsRentalAndReleasesReservation(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	repository := NewRentalRepository(pool)
	interval := rentalTestInterval(t, time.Date(2026, 9, 1, 13, 8, 0, 0, time.UTC))
	selection := []rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}}
	created, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.firstClientID, interval, selection)
	if err != nil {
		t.Fatalf("CreateConfirmed() error = %v", err)
	}
	cancelled, err := repository.Cancel(ctx, fixture.actor, created.ID)
	if err != nil {
		t.Fatalf("Cancel() error = %v", err)
	}
	if cancelled.Status != rental.StatusCancelled {
		t.Fatalf("Cancel() status = %q", cancelled.Status)
	}
	stored, err := repository.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertRentalEqual(t, stored, cancelled)

	var equipmentStatus equipment.Status
	if err := pool.QueryRow(ctx, "SELECT status FROM equipment WHERE id = $1", fixture.equipmentIDs[0]).Scan(&equipmentStatus); err != nil {
		t.Fatalf("query equipment status: %v", err)
	}
	if equipmentStatus != equipment.StatusAvailable {
		t.Fatalf("equipment status = %q", equipmentStatus)
	}
	var details string
	if err := pool.QueryRow(ctx, `SELECT details::text FROM audit_events
		WHERE action = 'rental.cancelled' AND target_id = $1`, created.ID).Scan(&details); err != nil {
		t.Fatalf("query cancelled audit: %v", err)
	}
	if !containsAll(details, `"client_id":`, `"equipment_count": 1`) &&
		!containsAll(details, `"client_id":`, `"equipment_count":1`) {
		t.Fatalf("cancelled audit details = %s", details)
	}
	replacement, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.secondClientID, interval, selection)
	if err != nil {
		t.Fatalf("CreateConfirmed() after cancellation error = %v", err)
	}

	confirmedPage, err := repository.ListPage(ctx, []rental.Status{rental.StatusConfirmed}, 1, 1000)
	if err != nil {
		t.Fatalf("ListPage(confirmed) error = %v", err)
	}
	historyPage, err := repository.ListPage(ctx, []rental.Status{rental.StatusCompleted, rental.StatusCancelled}, 1, 1000)
	if err != nil {
		t.Fatalf("ListPage(history) error = %v", err)
	}
	confirmedFound, cancelledFound := false, false
	for _, summary := range confirmedPage.Rentals {
		if summary.Status != rental.StatusConfirmed {
			t.Fatalf("confirmed page contains status %q", summary.Status)
		}
		confirmedFound = confirmedFound || summary.ID == replacement.ID
	}
	for _, summary := range historyPage.Rentals {
		if summary.Status != rental.StatusCompleted && summary.Status != rental.StatusCancelled {
			t.Fatalf("history page contains status %q", summary.Status)
		}
		cancelledFound = cancelledFound || summary.ID == created.ID
	}
	if !confirmedFound || !cancelledFound {
		t.Fatalf("confirmed page = %+v, history page = %+v", confirmedPage, historyPage)
	}
}

func TestRentalRepositoryCancellationIsConcurrentAndTransactional(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	repository := NewRentalRepository(pool)
	created, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.firstClientID,
		rentalTestInterval(t, time.Date(2026, 9, 1, 15, 8, 0, 0, time.UTC)),
		[]rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}})
	if err != nil {
		t.Fatalf("CreateConfirmed() error = %v", err)
	}

	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, cancelErr := repository.Cancel(ctx, fixture.actor, created.ID)
			errorsCh <- cancelErr
		}()
	}
	close(start)
	wait.Wait()
	close(errorsCh)
	successes, conflicts := 0, 0
	for cancelErr := range errorsCh {
		switch {
		case cancelErr == nil:
			successes++
		case errors.Is(cancelErr, rental.ErrStatusTransitionNotAllowed):
			conflicts++
		default:
			t.Fatalf("concurrent Cancel() error = %v", cancelErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes = %d conflicts = %d", successes, conflicts)
	}

	rollbackFixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	rollbackRepository := NewRentalRepository(pool)
	rollbackRental, err := rollbackRepository.CreateConfirmed(ctx, rollbackFixture.actor, rollbackFixture.firstClientID,
		rentalTestInterval(t, time.Date(2026, 9, 1, 17, 8, 0, 0, time.UTC)),
		[]rental.ModelSelection{{ModelID: rollbackFixture.modelID, Quantity: 1}})
	if err != nil {
		t.Fatalf("rollback CreateConfirmed() error = %v", err)
	}
	rollbackRepository.writeAudit = func(context.Context, pgx.Tx, string, user.User, rental.Rental, rentalAuditDetails) error {
		return errors.New("audit unavailable")
	}
	if _, err := rollbackRepository.Cancel(ctx, rollbackFixture.actor, rollbackRental.ID); err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("Cancel() audit error = %v", err)
	}
	stored, err := rollbackRepository.Get(ctx, rollbackRental.ID)
	if err != nil {
		t.Fatalf("Get() after rollback error = %v", err)
	}
	if stored.Status != rental.StatusConfirmed {
		t.Fatalf("status after rollback = %q", stored.Status)
	}
}

func TestRentalRepositoryIssueIsConcurrentAndTransactional(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	repository := NewRentalRepository(pool)
	created, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.firstClientID,
		rentalTestInterval(t, time.Date(2026, 9, 1, 14, 8, 0, 0, time.UTC)),
		[]rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}})
	if err != nil {
		t.Fatalf("CreateConfirmed() error = %v", err)
	}

	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, issueErr := repository.Issue(ctx, fixture.actor, created.ID, time.Now().UTC())
			errorsCh <- issueErr
		}()
	}
	close(start)
	wait.Wait()
	close(errorsCh)
	successes, conflicts := 0, 0
	for issueErr := range errorsCh {
		switch {
		case issueErr == nil:
			successes++
		case errors.Is(issueErr, rental.ErrStatusTransitionNotAllowed):
			conflicts++
		default:
			t.Fatalf("concurrent Issue() error = %v", issueErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes = %d conflicts = %d", successes, conflicts)
	}

	rollbackFixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	rollbackRepository := NewRentalRepository(pool)
	rollbackRental, err := rollbackRepository.CreateConfirmed(ctx, rollbackFixture.actor, rollbackFixture.firstClientID,
		rentalTestInterval(t, time.Date(2026, 9, 1, 16, 8, 0, 0, time.UTC)),
		[]rental.ModelSelection{{ModelID: rollbackFixture.modelID, Quantity: 1}})
	if err != nil {
		t.Fatalf("rollback CreateConfirmed() error = %v", err)
	}
	rollbackRepository.writeAudit = func(context.Context, pgx.Tx, string, user.User, rental.Rental, rentalAuditDetails) error {
		return errors.New("audit unavailable")
	}
	if _, err := rollbackRepository.Issue(ctx, rollbackFixture.actor, rollbackRental.ID, time.Now().UTC()); err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("Issue() audit error = %v", err)
	}
	var rentalStatus rental.Status
	var equipmentStatus equipment.Status
	if err := pool.QueryRow(ctx, "SELECT status FROM rentals WHERE id = $1", rollbackRental.ID).Scan(&rentalStatus); err != nil {
		t.Fatalf("query rolled back rental: %v", err)
	}
	if err := pool.QueryRow(ctx, "SELECT status FROM equipment WHERE id = $1", rollbackFixture.equipmentIDs[0]).Scan(&equipmentStatus); err != nil {
		t.Fatalf("query rolled back equipment: %v", err)
	}
	if rentalStatus != rental.StatusConfirmed || equipmentStatus != equipment.StatusAvailable {
		t.Fatalf("rollback statuses = %q, %q", rentalStatus, equipmentStatus)
	}
}

func TestRentalRepositoryRejectsIssueWithUnavailableEquipment(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	repository := NewRentalRepository(pool)
	created, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.firstClientID,
		rentalTestInterval(t, time.Date(2026, 9, 1, 18, 8, 0, 0, time.UTC)),
		[]rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}})
	if err != nil {
		t.Fatalf("CreateConfirmed() error = %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE equipment SET status = 'maintenance' WHERE id = $1", fixture.equipmentIDs[0]); err != nil {
		t.Fatalf("mark equipment maintenance: %v", err)
	}
	if _, err := repository.Issue(ctx, fixture.actor, created.ID, time.Now().UTC()); !errors.Is(err, rental.ErrEquipmentUnavailable) {
		t.Fatalf("Issue() error = %v, want ErrEquipmentUnavailable", err)
	}
	stored, err := repository.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Status != rental.StatusConfirmed {
		t.Fatalf("stored status = %q", stored.Status)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action = 'rental.issued' AND target_id = $1", created.ID).Scan(&count); err != nil {
		t.Fatalf("count issued audit: %v", err)
	}
	if count != 0 {
		t.Fatalf("issued audit events = %d", count)
	}
}

func TestRentalRepositoryRejectsUnavailableAndMissingReferences(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	repository := NewRentalRepository(pool)
	interval := rentalTestInterval(t, time.Date(2026, 9, 2, 10, 8, 0, 0, time.UTC))
	selection := []rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}}

	if _, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.firstClientID, interval, selection); err != nil {
		t.Fatalf("first CreateConfirmed() error = %v", err)
	}
	if _, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.secondClientID, interval, selection); !errors.Is(err, rental.ErrInsufficientEquipment) {
		t.Fatalf("overlap error = %v, want ErrInsufficientEquipment", err)
	}
	other := rentalTestInterval(t, interval.End())
	if _, err := repository.CreateConfirmed(ctx, fixture.actor, 9_000_000_000, other, selection); !errors.Is(err, client.ErrClientNotFound) {
		t.Fatalf("missing client error = %v, want ErrClientNotFound", err)
	}
	if _, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.secondClientID, other, []rental.ModelSelection{{ModelID: 9_000_000_000, Quantity: 1}}); !errors.Is(err, rental.ErrInsufficientEquipment) {
		t.Fatalf("missing model error = %v, want ErrInsufficientEquipment", err)
	}
}

func TestRentalRepositoryPreventsConcurrentDoubleBooking(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	repository := NewRentalRepository(pool)
	interval := rentalTestInterval(t, time.Date(2026, 9, 3, 10, 8, 0, 0, time.UTC))
	selection := []rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}}

	start := make(chan struct{})
	errorsCh := make(chan error, 2)
	var wait sync.WaitGroup
	for _, clientID := range []int64{fixture.firstClientID, fixture.secondClientID} {
		wait.Add(1)
		go func(clientID int64) {
			defer wait.Done()
			<-start
			_, err := repository.CreateConfirmed(ctx, fixture.actor, clientID, interval, selection)
			errorsCh <- err
		}(clientID)
	}
	close(start)
	wait.Wait()
	close(errorsCh)

	successes, conflicts := 0, 0
	for err := range errorsCh {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, rental.ErrInsufficientEquipment):
			conflicts++
		default:
			t.Fatalf("concurrent CreateConfirmed() error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes = %d conflicts = %d", successes, conflicts)
	}
}

func TestRentalRepositoryRollsBackWhenAuditFails(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	repository := NewRentalRepository(pool)
	repository.writeAudit = func(context.Context, pgx.Tx, string, user.User, rental.Rental, rentalAuditDetails) error {
		return errors.New("audit unavailable")
	}
	interval := rentalTestInterval(t, time.Date(2026, 9, 4, 10, 8, 0, 0, time.UTC))

	_, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.firstClientID, interval, []rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}})
	if err == nil || !containsAll(err.Error(), "audit unavailable") {
		t.Fatalf("CreateConfirmed() error = %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM rentals WHERE client_id = $1", fixture.firstClientID).Scan(&count); err != nil {
		t.Fatalf("count rentals: %v", err)
	}
	if count != 0 {
		t.Fatalf("rentals after rollback = %d", count)
	}
}

func TestRentalRepositoryAvailabilityAndList(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 3)
	repository := NewRentalRepository(pool)
	start := time.Date(2026, 9, 5, 10, 8, 0, 0, time.UTC)
	firstInterval := rentalTestInterval(t, start)
	first, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.firstClientID, firstInterval, []rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}})
	if err != nil {
		t.Fatalf("CreateConfirmed() error = %v", err)
	}
	available, err := repository.AvailableEquipment(ctx, firstInterval)
	if err != nil {
		t.Fatalf("AvailableEquipment() error = %v", err)
	}
	if countRentalFixtureEquipment(available, fixture.modelID) != 2 {
		t.Fatalf("available = %+v", available)
	}
	secondInterval := rentalTestInterval(t, start.Add(2*time.Hour))
	second, err := repository.CreateConfirmed(ctx, fixture.actor, fixture.secondClientID, secondInterval, []rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 2}})
	if err != nil {
		t.Fatalf("second CreateConfirmed() error = %v", err)
	}
	page, err := repository.ListPage(ctx, []rental.Status{rental.StatusConfirmed}, 1, 5)
	if err != nil {
		t.Fatalf("ListPage() error = %v", err)
	}
	found := map[int64]rental.Summary{}
	for _, summary := range page.Rentals {
		found[summary.ID] = summary
	}
	if found[first.ID].Status != rental.StatusConfirmed || found[second.ID].ItemCount != 2 || found[second.ID].PlannedTotalKopecks != 100_000 {
		t.Fatalf("summaries = %+v", found)
	}
}

func TestRentalRepositoryMonitoringReturnsOperationalSnapshot(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 4)
	repository := NewRentalRepository(pool)
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	dayStart := now.Truncate(24 * time.Hour)
	query := rental.MonitoringQuery{
		Now: now, DayStart: dayStart, DayEnd: dayStart.Add(24 * time.Hour), Limit: 1000,
	}
	baseline, err := repository.Monitoring(ctx, query)
	if err != nil {
		t.Fatalf("baseline Monitoring() error = %v", err)
	}

	confirmed, err := repository.CreateConfirmed(
		ctx, fixture.actor, fixture.firstClientID,
		rentalTestInterval(t, now.Add(30*time.Minute)),
		[]rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}},
	)
	if err != nil {
		t.Fatalf("create confirmed rental: %v", err)
	}
	active, err := repository.CreateConfirmed(
		ctx, fixture.actor, fixture.firstClientID,
		rentalTestInterval(t, now.Add(-30*time.Minute)),
		[]rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}},
	)
	if err != nil {
		t.Fatalf("create active rental: %v", err)
	}
	if _, err := repository.Issue(ctx, fixture.actor, active.ID, now.Add(-20*time.Minute)); err != nil {
		t.Fatalf("issue active rental: %v", err)
	}
	overdue, err := repository.CreateConfirmed(
		ctx, fixture.actor, fixture.secondClientID,
		rentalTestInterval(t, now.Add(-2*time.Hour)),
		[]rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}},
	)
	if err != nil {
		t.Fatalf("create overdue rental: %v", err)
	}
	if _, err := repository.Issue(ctx, fixture.actor, overdue.ID, now.Add(-2*time.Hour)); err != nil {
		t.Fatalf("issue overdue rental: %v", err)
	}
	cancelled, err := repository.CreateConfirmed(
		ctx, fixture.actor, fixture.secondClientID,
		rentalTestInterval(t, now.Add(3*time.Hour)),
		[]rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}},
	)
	if err != nil {
		t.Fatalf("create cancelled rental: %v", err)
	}
	if _, err := repository.Cancel(ctx, fixture.actor, cancelled.ID); err != nil {
		t.Fatalf("cancel rental: %v", err)
	}
	completed, err := repository.CreateConfirmed(
		ctx, fixture.actor, fixture.secondClientID,
		rentalTestInterval(t, now.Add(-4*time.Hour)),
		[]rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}},
	)
	if err != nil {
		t.Fatalf("create completed rental: %v", err)
	}
	if _, err := repository.Issue(ctx, fixture.actor, completed.ID, now.Add(-4*time.Hour)); err != nil {
		t.Fatalf("issue completed rental: %v", err)
	}
	if _, err := repository.Complete(ctx, fixture.actor, completed.ID, now.Add(-3*time.Hour)); err != nil {
		t.Fatalf("complete rental: %v", err)
	}

	data, err := repository.Monitoring(ctx, query)
	if err != nil {
		t.Fatalf("Monitoring() error = %v", err)
	}
	if data.TodayTotal != baseline.TodayTotal+3 ||
		data.ConfirmedTotal != baseline.ConfirmedTotal+1 ||
		data.ActiveTotal != baseline.ActiveTotal+2 ||
		data.OverdueTotal != baseline.OverdueTotal+1 {
		t.Fatalf("monitoring counts = %+v", data)
	}
	confirmedIndex := rentalSummaryIndex(data.Confirmed, confirmed.ID)
	if confirmedIndex < 0 {
		t.Fatalf("confirmed = %+v", data.Confirmed)
	}
	overdueIndex := rentalSummaryIndex(data.Active, overdue.ID)
	activeIndex := rentalSummaryIndex(data.Active, active.ID)
	if overdueIndex < 0 || activeIndex < 0 || overdueIndex >= activeIndex {
		t.Fatalf("active order = %+v", data.Active)
	}
}

func rentalSummaryIndex(summaries []rental.Summary, id int64) int {
	for index, summary := range summaries {
		if summary.ID == id {
			return index
		}
	}
	return -1
}

func TestRentalsTableRejectsRemovedDraftStatus(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	start := time.Date(2026, 9, 6, 10, 8, 0, 0, time.UTC)
	_, err := pool.Exec(ctx, `INSERT INTO rentals (client_id, planned_start_at, planned_end_at, status)
		VALUES ($1, $2, $3, 'draft')`, fixture.firstClientID, start, start.Add(time.Hour))
	assertPostgresCode(t, err, "23514")
}

func TestRentalsTableChecksLifecycleTimesAgainstStatus(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	start := time.Date(2026, 9, 6, 12, 8, 0, 0, time.UTC)
	_, err := pool.Exec(ctx, `INSERT INTO rentals (client_id, planned_start_at, planned_end_at, status)
		VALUES ($1, $2, $3, 'active')`, fixture.firstClientID, start, start.Add(time.Hour))
	assertPostgresCode(t, err, "23514")
	_, err = pool.Exec(ctx, `INSERT INTO rentals (client_id, planned_start_at, planned_end_at, status, issued_at)
		VALUES ($1, $2, $3, 'confirmed', $2)`, fixture.firstClientID, start, start.Add(time.Hour))
	assertPostgresCode(t, err, "23514")
	_, err = pool.Exec(ctx, `INSERT INTO rentals (client_id, planned_start_at, planned_end_at, status, issued_at)
		VALUES ($1, $2, $3, 'completed', $2)`, fixture.firstClientID, start, start.Add(time.Hour))
	assertPostgresCode(t, err, "23514")
	_, err = pool.Exec(ctx, `INSERT INTO rentals (
		client_id, planned_start_at, planned_end_at, status, issued_at, returned_at
	) VALUES ($1, $2, $3, 'completed', $3, $2)`, fixture.firstClientID, start, start.Add(time.Hour))
	assertPostgresCode(t, err, "23514")
}

func TestRentalItemPreventsEquipmentDeletion(t *testing.T) {
	pool, ctx := rentalTestPool(t)
	fixture := newRentalRepositoryFixture(t, ctx, pool, 1)
	interval := rentalTestInterval(t, time.Date(2026, 9, 7, 10, 8, 0, 0, time.UTC))
	if _, err := NewRentalRepository(pool).CreateConfirmed(ctx, fixture.actor, fixture.firstClientID, interval, []rental.ModelSelection{{ModelID: fixture.modelID, Quantity: 1}}); err != nil {
		t.Fatalf("CreateConfirmed() error = %v", err)
	}
	_, err := pool.Exec(ctx, "DELETE FROM equipment WHERE id = $1", fixture.equipmentIDs[0])
	assertPostgresCode(t, err, "23503")
}

type rentalRepositoryFixture struct {
	actor          user.User
	firstClientID  int64
	secondClientID int64
	modelID        int64
	modelCode      string
	equipmentIDs   []int64
}

func newRentalRepositoryFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, itemCount int) rentalRepositoryFixture {
	t.Helper()
	unique := time.Now().UnixNano()
	fixture := rentalRepositoryFixture{modelCode: fmt.Sprintf("RENTAL-%d", unique)}
	fixture.actor = user.User{Login: fmt.Sprintf("rental.%d", unique%1_000_000_000), Role: user.RoleOperator, Active: true}
	if err := pool.QueryRow(ctx, `INSERT INTO users (login, password_hash, role, active)
		VALUES ($1, 'test-hash', 'operator', true) RETURNING id`, fixture.actor.Login).Scan(&fixture.actor.ID); err != nil {
		t.Fatalf("insert rental actor: %v", err)
	}
	for index, target := range []*int64{&fixture.firstClientID, &fixture.secondClientID} {
		phone := fmt.Sprintf("+70%09d", (unique+int64(index))%1_000_000_000)
		if err := pool.QueryRow(ctx, `INSERT INTO clients (full_name, phone) VALUES ($1, $2) RETURNING id`, fmt.Sprintf("Rental Test Client %d", index+1), phone).Scan(target); err != nil {
			t.Fatalf("insert rental client: %v", err)
		}
	}
	if err := pool.QueryRow(ctx, `INSERT INTO equipment_models (kind, model_code, hourly_rate_kopecks)
		VALUES ('sup_board', $1, 50000) RETURNING id`, fixture.modelCode).Scan(&fixture.modelID); err != nil {
		t.Fatalf("insert rental model: %v", err)
	}
	for sequence := 1; sequence <= itemCount; sequence++ {
		var id int64
		if err := pool.QueryRow(ctx, `INSERT INTO equipment (model_id, sequence_number, status)
			VALUES ($1, $2, 'available') RETURNING id`, fixture.modelID, sequence).Scan(&id); err != nil {
			t.Fatalf("insert rental equipment: %v", err)
		}
		fixture.equipmentIDs = append(fixture.equipmentIDs, id)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM rentals WHERE client_id = ANY($1)", []int64{fixture.firstClientID, fixture.secondClientID})
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM audit_events WHERE actor_user_id = $1", fixture.actor.ID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM equipment WHERE model_id = $1", fixture.modelID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM equipment_models WHERE id = $1", fixture.modelID)
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM clients WHERE id = ANY($1)", []int64{fixture.firstClientID, fixture.secondClientID})
		_, _ = pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", fixture.actor.ID)
	})
	return fixture
}

func rentalTestInterval(t *testing.T, start time.Time) rental.Interval {
	t.Helper()
	interval, err := rental.NewInterval(start, start.Add(time.Hour))
	if err != nil {
		t.Fatalf("NewInterval() error = %v", err)
	}
	return interval
}

func countRentalFixtureEquipment(items []equipment.Item, modelID int64) int {
	count := 0
	for _, item := range items {
		if item.ModelID == modelID {
			count++
		}
	}
	return count
}

func assertRentalEqual(t *testing.T, got, want rental.Rental) {
	t.Helper()
	if got.ID != want.ID || got.ClientID != want.ClientID || got.Status != want.Status ||
		!got.Interval.Start().Equal(want.Interval.Start()) || !got.Interval.End().Equal(want.Interval.End()) ||
		fmt.Sprint(got.Items()) != fmt.Sprint(want.Items()) || !sameIssuedAt(got, want) || !sameReturnedAt(got, want) {
		t.Errorf("rental = %#v items %#v, want %#v items %#v", got, got.Items(), want, want.Items())
	}
}

func sameReturnedAt(first, second rental.Rental) bool {
	firstTime, firstOK := first.ReturnedAt()
	secondTime, secondOK := second.ReturnedAt()
	return firstOK == secondOK && (!firstOK || firstTime.Equal(secondTime))
}

func sameIssuedAt(first, second rental.Rental) bool {
	firstTime, firstOK := first.IssuedAt()
	secondTime, secondOK := second.IssuedAt()
	return firstOK == secondOK && (!firstOK || firstTime.Equal(secondTime))
}

func assertPostgresCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != wantCode {
		t.Fatalf("PostgreSQL error = %v, want code %s", err, wantCode)
	}
}

func rentalTestPool(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	connectionString := os.Getenv("TEST_DATABASE_URL")
	if connectionString == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := Open(ctx, connectionString)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !strings.Contains(value, part) {
			return false
		}
	}
	return true
}
