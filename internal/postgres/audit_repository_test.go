package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/audit"
)

func TestAuditRepositoryListFiltersAndOrdersEvents(t *testing.T) {
	connectionString := os.Getenv("TEST_DATABASE_URL")
	if connectionString == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := Open(ctx, connectionString)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)

	label := fmt.Sprintf("AUDIT-LIST-%d", time.Now().UnixNano())
	for _, action := range []string{"equipment.created", "equipment.updated"} {
		if _, err := pool.Exec(ctx, `INSERT INTO audit_events
			(actor_login, actor_role, action, target_type, target_label, result, details)
			VALUES ('audit.admin', 'admin', $1, 'equipment', $2, 'success', '{}')`, action, label); err != nil {
			t.Fatalf("insert audit fixture: %v; apply migrations first", err)
		}
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM audit_events WHERE target_label = $1", label); err != nil {
			t.Errorf("clean up audit fixtures: %v", err)
		}
	})

	filter, err := audit.NewFilter("equipment", "success", "audit.admin", label, nil, nil, 1)
	if err != nil {
		t.Fatalf("NewFilter() error = %v", err)
	}
	page, err := NewAuditRepository(pool).List(ctx, filter)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if page.Total != 2 || len(page.Events) != 2 || page.Events[0].Action != "equipment.updated" {
		t.Errorf("List() = %+v", page)
	}
}
