package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestSessionRepositoryLifecycle(t *testing.T) {
	pool, ctx := openUserRepositoryTestDatabase(t)
	account := createSessionTestUser(t, ctx, pool)
	repository := NewSessionRepository(pool)

	createdAt := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)
	digest := bytes.Repeat([]byte{1}, 32)
	created, err := repository.Create(ctx, session.CreateParams{
		UserID:            account.ID,
		TokenDigest:       digest,
		CSRFToken:         "csrf-token-one",
		CreatedAt:         createdAt,
		AbsoluteExpiresAt: createdAt.Add(session.AbsoluteLifetime),
	})
	if err != nil {
		t.Fatalf("Create() error = %v; apply migrations to TEST_DATABASE_URL first", err)
	}
	if created.ID == 0 || created.UserID != account.ID || created.CSRFToken != "csrf-token-one" {
		t.Errorf("Create() = %+v", created)
	}

	resolvedAt := createdAt.Add(time.Hour)
	resolved, err := repository.Resolve(ctx, digest, resolvedAt, session.IdleTimeout)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Session.ID != created.ID || !resolved.Session.LastSeenAt.Equal(resolvedAt) {
		t.Errorf("Resolve() session = %+v", resolved.Session)
	}
	if resolved.User != account {
		t.Errorf("Resolve() user = %+v, want %+v", resolved.User, account)
	}

	revokedAt := resolvedAt.Add(time.Minute)
	if err := repository.Revoke(ctx, digest, revokedAt); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if _, err := repository.Resolve(ctx, digest, revokedAt, session.IdleTimeout); !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("Resolve() revoked error = %v, want ErrSessionNotFound", err)
	}
	if err := repository.Revoke(ctx, digest, revokedAt); err != nil {
		t.Errorf("Revoke() repeated error = %v", err)
	}
}

func TestSessionRepositoryRejectsExpiredAndDisabledSessions(t *testing.T) {
	pool, ctx := openUserRepositoryTestDatabase(t)
	account := createSessionTestUser(t, ctx, pool)
	repository := NewSessionRepository(pool)
	createdAt := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		digest    []byte
		resolveAt time.Time
	}{
		{name: "idle timeout", digest: bytes.Repeat([]byte{2}, 32), resolveAt: createdAt.Add(session.IdleTimeout)},
		{name: "absolute expiry", digest: bytes.Repeat([]byte{3}, 32), resolveAt: createdAt.Add(session.AbsoluteLifetime)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := repository.Create(ctx, session.CreateParams{
				UserID:            account.ID,
				TokenDigest:       tt.digest,
				CSRFToken:         "csrf-" + tt.name,
				CreatedAt:         createdAt,
				AbsoluteExpiresAt: createdAt.Add(session.AbsoluteLifetime),
			})
			if err != nil {
				t.Fatalf("Create() error = %v", err)
			}

			_, err = repository.Resolve(ctx, tt.digest, tt.resolveAt, session.IdleTimeout)
			if !errors.Is(err, session.ErrSessionNotFound) {
				t.Errorf("Resolve() error = %v, want ErrSessionNotFound", err)
			}
		})
	}

	disabledDigest := bytes.Repeat([]byte{4}, 32)
	_, err := repository.Create(ctx, session.CreateParams{
		UserID:            account.ID,
		TokenDigest:       disabledDigest,
		CSRFToken:         "csrf-disabled",
		CreatedAt:         createdAt,
		AbsoluteExpiresAt: createdAt.Add(session.AbsoluteLifetime),
	})
	if err != nil {
		t.Fatalf("Create() disabled fixture error = %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET active = false WHERE id = $1", account.ID); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	if _, err := repository.Resolve(ctx, disabledDigest, createdAt.Add(time.Hour), session.IdleTimeout); !errors.Is(err, session.ErrSessionNotFound) {
		t.Errorf("Resolve() disabled user error = %v, want ErrSessionNotFound", err)
	}

	_, err = repository.Create(ctx, session.CreateParams{
		UserID:            account.ID,
		TokenDigest:       bytes.Repeat([]byte{5}, 32),
		CSRFToken:         "csrf-new-disabled",
		CreatedAt:         createdAt,
		AbsoluteExpiresAt: createdAt.Add(session.AbsoluteLifetime),
	})
	if !errors.Is(err, session.ErrUserUnavailable) {
		t.Errorf("Create() disabled user error = %v, want ErrUserUnavailable", err)
	}
}

func TestSessionRepositoryRevokeAll(t *testing.T) {
	pool, ctx := openUserRepositoryTestDatabase(t)
	account := createSessionTestUser(t, ctx, pool)
	repository := NewSessionRepository(pool)
	createdAt := time.Date(2026, time.August, 11, 8, 0, 0, 0, time.UTC)

	for index := byte(6); index < 8; index++ {
		_, err := repository.Create(ctx, session.CreateParams{
			UserID:            account.ID,
			TokenDigest:       bytes.Repeat([]byte{index}, 32),
			CSRFToken:         fmt.Sprintf("csrf-%d", index),
			CreatedAt:         createdAt,
			AbsoluteExpiresAt: createdAt.Add(session.AbsoluteLifetime),
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}

	revokedAt := createdAt.Add(time.Hour)
	if err := repository.RevokeAll(ctx, account.ID, revokedAt); err != nil {
		t.Fatalf("RevokeAll() error = %v", err)
	}

	var activeCount int
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL",
		account.ID,
	).Scan(&activeCount); err != nil {
		t.Fatalf("count active sessions: %v", err)
	}
	if activeCount != 0 {
		t.Errorf("active session count = %d, want 0", activeCount)
	}
}

func createSessionTestUser(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) user.User {
	t.Helper()

	account, err := user.New(
		fmt.Sprintf("session-%d", time.Now().UnixNano()),
		user.RoleOperator,
	)
	if err != nil {
		t.Fatalf("user.New() error = %v", err)
	}
	created, err := NewUserRepository(pool).Create(ctx, account, "encoded-password-hash")
	if err != nil {
		t.Fatalf("create session test user: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, err := pool.Exec(cleanupCtx, "DELETE FROM sessions WHERE user_id = $1", created.ID); err != nil {
			t.Errorf("clean up sessions: %v", err)
		}
		if _, err := pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", created.ID); err != nil {
			t.Errorf("clean up session test user: %v", err)
		}
	})

	return created
}
