package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/password"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestUserRepositoryCreateAndFindByLogin(t *testing.T) {
	pool, ctx := openUserRepositoryTestDatabase(t)
	repository := NewUserRepository(pool)

	login := fmt.Sprintf("operator-%d", time.Now().UnixNano())
	account, err := user.New(login, user.RoleOperator)
	if err != nil {
		t.Fatalf("user.New() error = %v", err)
	}
	passwordHash, err := password.NewHasher().Hash("operator-password")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	created, err := repository.Create(ctx, account, passwordHash)
	if err != nil {
		t.Fatalf("Create() error = %v; apply migrations to TEST_DATABASE_URL first", err)
	}
	cleanupUser(t, pool, created.ID)

	if created.ID == 0 {
		t.Error("Create() ID = 0, want generated ID")
	}
	if created.Login != login || created.Role != user.RoleOperator || !created.Active {
		t.Errorf("Create() = %+v, want active operator %q", created, login)
	}
	if created.LastLoginAt != nil {
		t.Errorf("Create() LastLoginAt = %v, want nil", created.LastLoginAt)
	}

	found, foundPasswordHash, err := repository.FindByLogin(ctx, login)
	if err != nil {
		t.Fatalf("FindByLogin() error = %v", err)
	}
	if found != created {
		t.Errorf("FindByLogin() user = %+v, want %+v", found, created)
	}
	if foundPasswordHash != passwordHash {
		t.Error("FindByLogin() returned a different password hash")
	}

	_, err = repository.Create(ctx, account, passwordHash)
	if !errors.Is(err, user.ErrLoginExists) {
		t.Errorf("duplicate Create() error = %v, want ErrLoginExists", err)
	}

	_, _, err = repository.FindByLogin(ctx, login+"-missing")
	if !errors.Is(err, user.ErrUserNotFound) {
		t.Errorf("missing FindByLogin() error = %v, want ErrUserNotFound", err)
	}
}

func TestUserRepositoryAllowsOnlyOneAdmin(t *testing.T) {
	pool, ctx := openUserRepositoryTestDatabase(t)
	repository := NewUserRepository(pool)

	var adminExists bool
	if err := pool.QueryRow(
		ctx,
		"SELECT EXISTS (SELECT 1 FROM users WHERE role = 'admin')",
	).Scan(&adminExists); err != nil {
		t.Fatalf("check existing admin: %v; apply migrations to TEST_DATABASE_URL first", err)
	}

	passwordHash, err := password.NewHasher().Hash("admin-password")
	if err != nil {
		t.Fatalf("Hash() error = %v", err)
	}

	if !adminExists {
		firstAdmin, err := user.New(
			fmt.Sprintf("admin-%d", time.Now().UnixNano()),
			user.RoleAdmin,
		)
		if err != nil {
			t.Fatalf("user.New() first admin error = %v", err)
		}

		created, err := repository.Create(ctx, firstAdmin, passwordHash)
		if err != nil {
			t.Fatalf("Create() first admin error = %v", err)
		}
		cleanupUser(t, pool, created.ID)
	}

	secondAdmin, err := user.New(
		fmt.Sprintf("admin-%d", time.Now().UnixNano()+1),
		user.RoleAdmin,
	)
	if err != nil {
		t.Fatalf("user.New() second admin error = %v", err)
	}

	_, err = repository.Create(ctx, secondAdmin, passwordHash)
	if !errors.Is(err, user.ErrAdminExists) {
		t.Errorf("second admin Create() error = %v, want ErrAdminExists", err)
	}
}

func openUserRepositoryTestDatabase(t *testing.T) (*pgxpool.Pool, context.Context) {
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

func cleanupUser(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", id); err != nil {
			t.Errorf("clean up user: %v", err)
		}
	})
}
