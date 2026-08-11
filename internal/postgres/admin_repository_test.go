package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/password"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAdminRepositoryCreateAndResetPasswordWithAudit(t *testing.T) {
	pool, ctx := openUserRepositoryTestDatabase(t)
	skipWhenAdminExists(t, ctx, pool)

	repository := NewAdminRepository(pool)
	login := fmt.Sprintf("admin-%d", time.Now().UnixNano())
	account, err := user.New(login, user.RoleAdmin)
	if err != nil {
		t.Fatalf("user.New() error = %v", err)
	}

	oldHash, err := password.NewHasher().Hash("old-password")
	if err != nil {
		t.Fatalf("Hash() old password error = %v", err)
	}
	created, err := repository.CreateAdmin(ctx, account, oldHash)
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v; apply migrations to TEST_DATABASE_URL first", err)
	}
	cleanupAdminFixture(t, pool, created.ID)
	insertAdminSessionFixture(t, ctx, pool, created.ID, 20)

	newHash, err := password.NewHasher().Hash("new-password")
	if err != nil {
		t.Fatalf("Hash() new password error = %v", err)
	}
	reset, err := repository.ResetAdminPassword(ctx, newHash)
	if err != nil {
		t.Fatalf("ResetAdminPassword() error = %v", err)
	}
	if reset != created {
		t.Errorf("ResetAdminPassword() = %+v, want %+v", reset, created)
	}

	var storedHash string
	if err := pool.QueryRow(
		ctx,
		"SELECT password_hash FROM users WHERE id = $1",
		created.ID,
	).Scan(&storedHash); err != nil {
		t.Fatalf("query updated password hash: %v", err)
	}
	if storedHash != newHash || storedHash == oldHash {
		t.Error("stored password hash was not replaced")
	}
	assertAdminSessionsRevoked(t, ctx, pool, created.ID, true)

	rows, err := pool.Query(
		ctx,
		`SELECT action, actor_user_id, actor_login, actor_role,
		        target_type, target_id, target_label, result, details->>'source'
		 FROM audit_events
		 WHERE target_id = $1
		 ORDER BY id`,
		created.ID,
	)
	if err != nil {
		t.Fatalf("query admin audit events: %v", err)
	}
	defer rows.Close()

	wantActions := []string{actionAdminCreated, actionAdminPasswordChanged}
	var index int
	for rows.Next() {
		var action, targetType, targetLabel, result, source string
		var actorID *int64
		var actorLogin, actorRole *string
		var targetID int64
		if err := rows.Scan(
			&action,
			&actorID,
			&actorLogin,
			&actorRole,
			&targetType,
			&targetID,
			&targetLabel,
			&result,
			&source,
		); err != nil {
			t.Fatalf("scan admin audit event: %v", err)
		}
		if index >= len(wantActions) {
			t.Fatalf("unexpected extra audit event %q", action)
		}
		if action != wantActions[index] || actorID != nil || actorLogin != nil ||
			actorRole != nil || targetType != "user" || targetID != created.ID ||
			targetLabel != created.Login || result != "success" || source != "admin_cli" {
			t.Errorf("audit event has unexpected values for action %q", action)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate admin audit events: %v", err)
	}
	if index != len(wantActions) {
		t.Errorf("audit event count = %d, want %d", index, len(wantActions))
	}
}

func TestAdminRepositoryRollsBackWhenAuditFails(t *testing.T) {
	pool, ctx := openUserRepositoryTestDatabase(t)
	skipWhenAdminExists(t, ctx, pool)

	repository := NewAdminRepository(pool)
	repository.writeAudit = func(context.Context, pgx.Tx, string, user.User) error {
		return errors.New("audit unavailable")
	}
	login := fmt.Sprintf("ar-%d", time.Now().UnixNano())
	account, err := user.New(login, user.RoleAdmin)
	if err != nil {
		t.Fatalf("user.New() error = %v", err)
	}

	_, err = repository.CreateAdmin(ctx, account, "encoded-password-hash")
	if err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("CreateAdmin() error = %v, want audit failure", err)
	}

	var count int
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM users WHERE login = $1",
		login,
	).Scan(&count); err != nil {
		t.Fatalf("query rolled back admin: %v", err)
	}
	if count != 0 {
		t.Errorf("rolled back admin count = %d, want 0", count)
	}
}

func TestAdminRepositoryRollsBackPasswordWhenAuditFails(t *testing.T) {
	pool, ctx := openUserRepositoryTestDatabase(t)
	skipWhenAdminExists(t, ctx, pool)

	account, err := user.New(
		fmt.Sprintf("ap-%d", time.Now().UnixNano()),
		user.RoleAdmin,
	)
	if err != nil {
		t.Fatalf("user.New() error = %v", err)
	}

	repository := NewAdminRepository(pool)
	created, err := repository.CreateAdmin(ctx, account, "old-password-hash")
	if err != nil {
		t.Fatalf("CreateAdmin() error = %v", err)
	}
	cleanupAdminFixture(t, pool, created.ID)
	insertAdminSessionFixture(t, ctx, pool, created.ID, 21)

	repository.writeAudit = func(context.Context, pgx.Tx, string, user.User) error {
		return errors.New("audit unavailable")
	}
	_, err = repository.ResetAdminPassword(ctx, "new-password-hash")
	if err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("ResetAdminPassword() error = %v, want audit failure", err)
	}

	var storedHash string
	if err := pool.QueryRow(
		ctx,
		"SELECT password_hash FROM users WHERE id = $1",
		created.ID,
	).Scan(&storedHash); err != nil {
		t.Fatalf("query password hash after rollback: %v", err)
	}
	if storedHash != "old-password-hash" {
		t.Errorf("password hash after rollback = %q, want old hash", storedHash)
	}
	assertAdminSessionsRevoked(t, ctx, pool, created.ID, false)
}

func TestAdminRepositoryResetRequiresAdmin(t *testing.T) {
	pool, ctx := openUserRepositoryTestDatabase(t)
	skipWhenAdminExists(t, ctx, pool)

	repository := NewAdminRepository(pool)
	_, err := repository.ResetAdminPassword(ctx, "encoded-password-hash")
	if !errors.Is(err, user.ErrUserNotFound) {
		t.Fatalf("ResetAdminPassword() error = %v, want ErrUserNotFound", err)
	}
}

func skipWhenAdminExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	var exists bool
	if err := pool.QueryRow(
		ctx,
		"SELECT EXISTS (SELECT 1 FROM users WHERE role = 'admin')",
	).Scan(&exists); err != nil {
		t.Fatalf("check existing admin: %v; apply migrations to TEST_DATABASE_URL first", err)
	}
	if exists {
		t.Skip("TEST_DATABASE_URL contains a permanent admin")
	}
}

func cleanupAdminFixture(t *testing.T, pool *pgxpool.Pool, id int64) {
	t.Helper()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, err := pool.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", id); err != nil {
			t.Errorf("clean up admin sessions: %v", err)
		}
		if _, err := pool.Exec(ctx, "DELETE FROM audit_events WHERE target_id = $1", id); err != nil {
			t.Errorf("clean up admin audit events: %v", err)
		}
		if _, err := pool.Exec(ctx, "DELETE FROM users WHERE id = $1", id); err != nil {
			t.Errorf("clean up admin: %v", err)
		}
	})
}

func insertAdminSessionFixture(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	userID int64,
	digestByte byte,
) {
	t.Helper()

	_, err := pool.Exec(
		ctx,
		`INSERT INTO sessions (
			user_id, token_digest, csrf_token, created_at, last_seen_at,
			absolute_expires_at
		) VALUES ($1, $2, 'admin-csrf', now(), now(), now() + interval '24 hours')`,
		userID,
		bytes.Repeat([]byte{digestByte}, 32),
	)
	if err != nil {
		t.Fatalf("insert admin session fixture: %v; apply migrations to TEST_DATABASE_URL first", err)
	}
}

func assertAdminSessionsRevoked(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	userID int64,
	wantRevoked bool,
) {
	t.Helper()

	var revoked bool
	if err := pool.QueryRow(
		ctx,
		"SELECT revoked_at IS NOT NULL FROM sessions WHERE user_id = $1",
		userID,
	).Scan(&revoked); err != nil {
		t.Fatalf("query admin session revocation: %v", err)
	}
	if revoked != wantRevoked {
		t.Errorf("admin session revoked = %t, want %t", revoked, wantRevoked)
	}
}
