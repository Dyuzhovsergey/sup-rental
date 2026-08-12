package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestClientRepositoryCreateGetAndFindByPhone(t *testing.T) {
	pool, ctx := openClientRepositoryTestDatabase(t)
	repository := NewClientRepository(pool)
	phone := fmt.Sprintf("+79%09d", time.Now().UnixNano()%1_000_000_000)
	customer, err := client.New("  Тестовый   Клиент  ", phone)
	if err != nil {
		t.Fatalf("client.New() error = %v", err)
	}

	created, err := repository.Create(ctx, customer)
	if err != nil {
		t.Fatalf("Create() error = %v; apply migrations to TEST_DATABASE_URL first", err)
	}
	cleanupClient(t, pool, created.ID)

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
	if _, err := repository.Create(ctx, duplicate); !errors.Is(err, client.ErrPhoneExists) {
		t.Errorf("duplicate Create() error = %v, want ErrPhoneExists", err)
	}

	if _, err := repository.Get(ctx, created.ID+1_000_000); !errors.Is(err, client.ErrClientNotFound) {
		t.Errorf("missing Get() error = %v, want ErrClientNotFound", err)
	}
	if _, err := repository.FindByPhone(ctx, client.Phone("+100000000000000")); !errors.Is(err, client.ErrClientNotFound) {
		t.Errorf("missing FindByPhone() error = %v, want ErrClientNotFound", err)
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

func cleanupClient(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(ctx, "DELETE FROM clients WHERE id = $1", id); err != nil {
			t.Errorf("clean up client: %v", err)
		}
	})
}
