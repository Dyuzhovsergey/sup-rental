package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestClientRepositoryCreateGetAndFindByPhone(t *testing.T) {
	pool, ctx := openClientRepositoryTestDatabase(t)
	repository := NewClientRepository(pool)
	actor := clientTestOperator(t, ctx, pool)
	phone := fmt.Sprintf("+79%09d", time.Now().UnixNano()%1_000_000_000)
	customer, err := client.New("  Тестовый   Клиент  ", phone)
	if err != nil {
		t.Fatalf("client.New() error = %v", err)
	}

	created, err := repository.Create(ctx, actor, customer)
	if err != nil {
		t.Fatalf("Create() error = %v; apply migrations to TEST_DATABASE_URL first", err)
	}
	cleanupClient(t, pool, created.ID, actor.ID)

	if created.ID == 0 || created.FullName != "Тестовый Клиент" || created.Phone != customer.Phone {
		t.Errorf("Create() = %+v", created)
	}

	got, err := repository.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != created {
		t.Errorf("Get() = %+v, want %+v", got, created)
	}

	found, err := repository.FindByPhone(ctx, created.Phone)
	if err != nil {
		t.Fatalf("FindByPhone() error = %v", err)
	}
	if found != created {
		t.Errorf("FindByPhone() = %+v, want %+v", found, created)
	}

	duplicate, err := client.New("Другой Клиент", phone)
	if err != nil {
		t.Fatalf("client.New() duplicate error = %v", err)
	}
	if _, err := repository.Create(ctx, actor, duplicate); !errors.Is(err, client.ErrPhoneExists) {
		t.Errorf("duplicate Create() error = %v, want ErrPhoneExists", err)
	}

	if _, err := repository.Get(ctx, created.ID+1_000_000); !errors.Is(err, client.ErrClientNotFound) {
		t.Errorf("missing Get() error = %v, want ErrClientNotFound", err)
	}
	if _, err := repository.FindByPhone(ctx, client.Phone("+100000000000000")); !errors.Is(err, client.ErrClientNotFound) {
		t.Errorf("missing FindByPhone() error = %v, want ErrClientNotFound", err)
	}

	page, err := repository.ListPage(ctx, 1, 10)
	if err != nil {
		t.Fatalf("ListPage() error = %v", err)
	}
	if page.Total < 1 || len(page.Clients) == 0 {
		t.Errorf("ListPage() = %+v", page)
	}

	var action, actorLogin, actorRole, targetType, targetLabel, details string
	if err := pool.QueryRow(ctx, `SELECT action, actor_login, actor_role, target_type, target_label, details::text
		FROM audit_events WHERE action = 'client.created' AND target_id = $1`, created.ID).Scan(
		&action, &actorLogin, &actorRole, &targetType, &targetLabel, &details,
	); err != nil {
		t.Fatalf("query client audit event: %v", err)
	}
	if action != "client.created" || actorLogin != actor.Login || actorRole != "operator" || targetType != "client" || targetLabel != created.FullName || details != "{}" {
		t.Errorf("audit = %q %q %q %q %q %q", action, actorLogin, actorRole, targetType, targetLabel, details)
	}
}

func TestClientRepositoryUpdateAndAudit(t *testing.T) {
	pool, ctx := openClientRepositoryTestDatabase(t)
	repository := NewClientRepository(pool)
	actor := clientTestOperator(t, ctx, pool)
	phone := fmt.Sprintf("+76%09d", time.Now().UnixNano()%1_000_000_000)
	created, err := repository.Create(ctx, actor, client.Client{
		FullName: "Клиент До Изменения", Phone: client.Phone(phone),
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	cleanupClient(t, pool, created.ID, actor.ID)

	updatedPhone := client.Phone(fmt.Sprintf("+75%09d", time.Now().UnixNano()%1_000_000_000))
	updated, err := repository.Update(ctx, actor, client.Client{
		ID: created.ID, FullName: "Клиент После Изменения", Phone: updatedPhone,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.ID != created.ID || updated.FullName != "Клиент После Изменения" || updated.Phone != updatedPhone {
		t.Errorf("Update() = %+v", updated)
	}

	stored, err := repository.Get(ctx, created.ID)
	if err != nil || stored != updated {
		t.Errorf("Get() = %+v, %v; want %+v", stored, err, updated)
	}

	var action, actorLogin, targetLabel, details string
	if err := pool.QueryRow(ctx, `SELECT action, actor_login, target_label, details::text
		FROM audit_events WHERE action = 'client.updated' AND target_id = $1`, created.ID).Scan(
		&action, &actorLogin, &targetLabel, &details,
	); err != nil {
		t.Fatalf("query update audit event: %v", err)
	}
	if action != "client.updated" || actorLogin != actor.Login || targetLabel != updated.FullName {
		t.Errorf("audit = %q %q %q", action, actorLogin, targetLabel)
	}
	for _, want := range []string{
		`"before_full_name": "Клиент До Изменения"`,
		`"after_full_name": "Клиент После Изменения"`,
		`"phone_changed": true`,
	} {
		if !strings.Contains(details, want) {
			t.Errorf("details = %q, want %q", details, want)
		}
	}
	if strings.Contains(details, phone) || strings.Contains(details, string(updatedPhone)) {
		t.Errorf("audit details expose phone: %q", details)
	}
}

func TestClientRepositoryUpdateRejectsDuplicatePhone(t *testing.T) {
	pool, ctx := openClientRepositoryTestDatabase(t)
	repository := NewClientRepository(pool)
	actor := clientTestOperator(t, ctx, pool)
	firstPhone := fmt.Sprintf("+74%09d", time.Now().UnixNano()%1_000_000_000)
	secondPhone := fmt.Sprintf("+73%09d", (time.Now().UnixNano()+1)%1_000_000_000)
	first, err := repository.Create(ctx, actor, client.Client{FullName: "Первый Клиент", Phone: client.Phone(firstPhone)})
	if err != nil {
		t.Fatalf("create first client: %v", err)
	}
	cleanupClient(t, pool, first.ID, actor.ID)
	second, err := repository.Create(ctx, actor, client.Client{FullName: "Второй Клиент", Phone: client.Phone(secondPhone)})
	if err != nil {
		t.Fatalf("create second client: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(ctx, "DELETE FROM audit_events WHERE target_type = 'client' AND target_id = $1", second.ID)
		_, _ = pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", second.ID)
	})

	_, err = repository.Update(ctx, actor, client.Client{
		ID: first.ID, FullName: "Первый Клиент", Phone: second.Phone,
	})
	if !errors.Is(err, client.ErrPhoneExists) {
		t.Fatalf("Update() error = %v, want ErrPhoneExists", err)
	}
	stored, getErr := repository.Get(ctx, first.ID)
	if getErr != nil || stored.Phone != first.Phone {
		t.Errorf("stored after conflict = %+v, %v", stored, getErr)
	}
}

func TestClientRepositoryRollsBackUpdateWhenAuditFails(t *testing.T) {
	pool, ctx := openClientRepositoryTestDatabase(t)
	actor := clientTestOperator(t, ctx, pool)
	repository := NewClientRepository(pool)
	phone := fmt.Sprintf("+72%09d", time.Now().UnixNano()%1_000_000_000)
	created, err := repository.Create(ctx, actor, client.Client{FullName: "Исходный Клиент", Phone: client.Phone(phone)})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	cleanupClient(t, pool, created.ID, actor.ID)
	repository.writeAudit = func(context.Context, pgx.Tx, string, user.User, client.Client, clientAuditDetails) error {
		return errors.New("audit unavailable")
	}

	_, err = repository.Update(ctx, actor, client.Client{
		ID: created.ID, FullName: "Изменённый Клиент", Phone: created.Phone,
	})
	if err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("Update() error = %v", err)
	}
	stored, getErr := repository.Get(ctx, created.ID)
	if getErr != nil || stored != created {
		t.Errorf("stored after rollback = %+v, %v; want %+v", stored, getErr, created)
	}
}

func TestClientRepositoryUpdateMissingClient(t *testing.T) {
	pool, ctx := openClientRepositoryTestDatabase(t)
	repository := NewClientRepository(pool)
	actor := clientTestOperator(t, ctx, pool)
	t.Cleanup(func() { cleanupClientOperator(t, pool, actor.ID) })

	_, err := repository.Update(ctx, actor, client.Client{
		ID: 9_000_000_000, FullName: "Нет Клиента", Phone: "+79990000000",
	})
	if !errors.Is(err, client.ErrClientNotFound) {
		t.Errorf("Update() error = %v, want ErrClientNotFound", err)
	}
}

func TestClientRepositoryRollsBackCreateWhenAuditFails(t *testing.T) {
	pool, ctx := openClientRepositoryTestDatabase(t)
	actor := clientTestOperator(t, ctx, pool)
	t.Cleanup(func() { cleanupClientOperator(t, pool, actor.ID) })
	repository := NewClientRepository(pool)
	repository.writeAudit = func(context.Context, pgx.Tx, string, user.User, client.Client, clientAuditDetails) error {
		return errors.New("audit unavailable")
	}
	phone := fmt.Sprintf("+78%09d", time.Now().UnixNano()%1_000_000_000)
	customer, err := client.New("Клиент Отката", phone)
	if err != nil {
		t.Fatalf("client.New() error = %v", err)
	}
	if _, err := repository.Create(ctx, actor, customer); err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("Create() error = %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM clients WHERE phone = $1", customer.Phone).Scan(&count); err != nil {
		t.Fatalf("count rolled back client: %v", err)
	}
	if count != 0 {
		t.Errorf("client count after rollback = %d", count)
	}
}

func TestClientsTableRejectsInvalidPersistentData(t *testing.T) {
	pool, ctx := openClientRepositoryTestDatabase(t)

	tests := []struct {
		name, fullName, phone string
	}{
		{name: "empty full name", fullName: "", phone: "+79990000001"},
		{name: "untrimmed full name", fullName: " Клиент ", phone: "+79990000002"},
		{name: "invalid phone", fullName: "Клиент", phone: "89990000003"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, "INSERT INTO clients (full_name, phone) VALUES ($1, $2)", tt.fullName, tt.phone)
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) || postgresError.Code != "23514" {
				t.Fatalf("INSERT error = %v, want PostgreSQL check violation 23514", err)
			}
		})
	}
}

func openClientRepositoryTestDatabase(t *testing.T) (*pgxpool.Pool, context.Context) {
	t.Helper()
	connectionString := os.Getenv("TEST_DATABASE_URL")
	if connectionString == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	pool, err := Open(ctx, connectionString)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)
	return pool, ctx
}

func cleanupClient(t *testing.T, pool *pgxpool.Pool, id, actorID int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(ctx, "DELETE FROM audit_events WHERE target_type = 'client' AND target_id = $1", id)
		if _, err := pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", id); err != nil {
			t.Errorf("clean up client: %v", err)
		}
		cleanupClientOperator(t, pool, actorID)
	})
}

func clientTestOperator(t *testing.T, ctx context.Context, pool *pgxpool.Pool) user.User {
	t.Helper()
	account := user.User{Login: fmt.Sprintf("clientop%d", time.Now().UnixNano()%1_000_000_000_000), Role: user.RoleOperator, Active: true}
	if err := pool.QueryRow(ctx, `INSERT INTO users (login, password_hash, role, active)
		VALUES ($1, 'test-hash', 'operator', true) RETURNING id`, account.Login).Scan(&account.ID); err != nil {
		t.Fatalf("insert client test operator: %v", err)
	}
	return account
}

func cleanupClientOperator(t *testing.T, pool *pgxpool.Pool, actorID int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _ = pool.Exec(ctx, "DELETE FROM audit_events WHERE actor_user_id = $1", actorID)
	_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", actorID)
}
