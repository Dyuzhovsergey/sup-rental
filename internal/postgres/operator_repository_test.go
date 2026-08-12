package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOperatorRepositoryLifecycleWithAuditAndSessionRevocation(t *testing.T) {
	pool, ctx := openUserRepositoryTestDatabase(t)
	actor, actorCreated := operatorTestAdmin(t, ctx, pool)
	repository := NewOperatorRepository(pool)
	account, err := user.New(fmt.Sprintf("managed-%d", time.Now().UnixNano()), user.RoleOperator)
	if err != nil {
		t.Fatalf("user.New() error = %v", err)
	}

	created, err := repository.CreateOperator(ctx, actor, account, "first-password-hash")
	if err != nil {
		t.Fatalf("CreateOperator() error = %v; apply migrations to TEST_DATABASE_URL first", err)
	}
	cleanupOperatorFixture(t, pool, created.ID, actor, actorCreated)
	insertOperatorSessionFixture(t, ctx, pool, created.ID, 61)

	listed, err := repository.ListOperators(ctx)
	if err != nil {
		t.Fatalf("ListOperators() error = %v", err)
	}
	if !containsUser(listed, created.ID) {
		t.Errorf("ListOperators() does not contain %d", created.ID)
	}

	disabled, err := repository.SetOperatorActive(ctx, actor, created.ID, false)
	if err != nil || disabled.Active {
		t.Fatalf("disable = %+v, error = %v", disabled, err)
	}
	assertOperatorSessionsRevoked(t, ctx, pool, created.ID, true)

	activated, err := repository.SetOperatorActive(ctx, actor, created.ID, true)
	if err != nil || !activated.Active {
		t.Fatalf("activate = %+v, error = %v", activated, err)
	}
	insertOperatorSessionFixture(t, ctx, pool, created.ID, 62)

	if _, err := repository.ChangeOperatorPassword(ctx, actor, created.ID, "second-password-hash"); err != nil {
		t.Fatalf("ChangeOperatorPassword() error = %v", err)
	}
	assertOperatorSessionsRevoked(t, ctx, pool, created.ID, true)

	var storedHash string
	if err := pool.QueryRow(ctx, "SELECT password_hash FROM users WHERE id = $1", created.ID).Scan(&storedHash); err != nil {
		t.Fatalf("query password hash: %v", err)
	}
	if storedHash != "second-password-hash" {
		t.Errorf("stored hash = %q", storedHash)
	}

	rows, err := pool.Query(ctx, `SELECT action, actor_user_id, actor_login, actor_role, target_label
		FROM audit_events WHERE target_type = 'user' AND target_id = $1 ORDER BY id`, created.ID)
	if err != nil {
		t.Fatalf("query audit events: %v", err)
	}
	defer rows.Close()
	want := []string{actionOperatorCreated, actionOperatorDisabled, actionOperatorActivated, actionOperatorPasswordChanged}
	index := 0
	for rows.Next() {
		var action, actorLogin, actorRole, targetLabel string
		var actorID int64
		if err := rows.Scan(&action, &actorID, &actorLogin, &actorRole, &targetLabel); err != nil {
			t.Fatalf("scan audit event: %v", err)
		}
		if index >= len(want) || action != want[index] || actorID != actor.ID || actorLogin != actor.Login || actorRole != string(user.RoleAdmin) || targetLabel != created.Login {
			t.Errorf("unexpected audit event %d: %q actor=%d/%q/%q target=%q", index, action, actorID, actorLogin, actorRole, targetLabel)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate audit events: %v", err)
	}
	if index != len(want) {
		t.Errorf("audit count = %d, want %d", index, len(want))
	}
}

func TestOperatorRepositoryRollsBackWhenAuditFails(t *testing.T) {
	pool, ctx := openUserRepositoryTestDatabase(t)
	actor, actorCreated := operatorTestAdmin(t, ctx, pool)
	repository := NewOperatorRepository(pool)
	repository.writeAudit = func(context.Context, pgx.Tx, string, user.User, user.User) error {
		return errors.New("audit unavailable")
	}
	account, err := user.New(fmt.Sprintf("rollback-%d", time.Now().UnixNano()), user.RoleOperator)
	if err != nil {
		t.Fatalf("user.New() error = %v", err)
	}
	if actorCreated {
		t.Cleanup(func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_, _ = pool.Exec(cleanupCtx, "DELETE FROM users WHERE id = $1", actor.ID)
		})
	}

	_, err = repository.CreateOperator(ctx, actor, account, "password-hash")
	if err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("CreateOperator() error = %v, want audit failure", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM users WHERE login = $1", account.Login).Scan(&count); err != nil {
		t.Fatalf("count rolled back operator: %v", err)
	}
	if count != 0 {
		t.Errorf("operator count after rollback = %d, want 0", count)
	}
}

func operatorTestAdmin(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (user.User, bool) {
	t.Helper()
	var actor user.User
	err := pool.QueryRow(ctx, `SELECT id, login, role, active, last_login_at FROM users WHERE role = 'admin'`).Scan(
		&actor.ID, &actor.Login, &actor.Role, &actor.Active, &actor.LastLoginAt,
	)
	if err == nil {
		return actor, false
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("find test admin: %v", err)
	}
	actor, err = user.New(fmt.Sprintf("admin-%d", time.Now().UnixNano()), user.RoleAdmin)
	if err != nil {
		t.Fatalf("user.New() admin error = %v", err)
	}
	err = pool.QueryRow(ctx, `INSERT INTO users (login, password_hash, role, active) VALUES ($1, 'test-hash', 'admin', true) RETURNING id`, actor.Login).Scan(&actor.ID)
	if err != nil {
		t.Fatalf("insert test admin: %v", err)
	}
	return actor, true
}

func cleanupOperatorFixture(t *testing.T, pool *pgxpool.Pool, operatorID int64, actor user.User, actorCreated bool) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = pool.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", operatorID)
		_, _ = pool.Exec(ctx, "DELETE FROM audit_events WHERE target_type = 'user' AND target_id = $1", operatorID)
		_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", operatorID)
		if actorCreated {
			_, _ = pool.Exec(ctx, "DELETE FROM users WHERE id = $1", actor.ID)
		}
	})
}

func insertOperatorSessionFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID int64, marker byte) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO sessions (user_id, token_digest, csrf_token, created_at, last_seen_at, absolute_expires_at)
		VALUES ($1, $2, 'operator-csrf', now(), now(), now() + interval '24 hours')`, userID, bytes.Repeat([]byte{marker}, 32))
	if err != nil {
		t.Fatalf("insert operator session: %v", err)
	}
}

func assertOperatorSessionsRevoked(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID int64, want bool) {
	t.Helper()
	var active int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM sessions WHERE user_id = $1 AND revoked_at IS NULL", userID).Scan(&active); err != nil {
		t.Fatalf("count active sessions: %v", err)
	}
	if got := active == 0; got != want {
		t.Errorf("all sessions revoked = %t, want %t", got, want)
	}
}

func containsUser(accounts []user.User, id int64) bool {
	for _, account := range accounts {
		if account.ID == id {
			return true
		}
	}
	return false
}
