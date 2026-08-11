package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/auth"
	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAuthRepositoryLoginLogoutAndAudit(t *testing.T) {
	pool, ctx := openUserRepositoryTestDatabase(t)
	account := createSessionTestUser(t, ctx, pool)
	repository := NewAuthRepository(pool)
	cleanupAuthFixtures(t, pool, account.ID)

	storedAccount, passwordHash, err := repository.FindByLogin(ctx, account.Login)
	if err != nil {
		t.Fatalf("FindByLogin() error = %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	attempt := auth.Attempt{
		LoginKey:   bytes.Repeat([]byte{30}, 32),
		IPKey:      bytes.Repeat([]byte{31}, 32),
		LoginLabel: account.Login,
		RemoteIP:   "127.0.0.1",
		OccurredAt: now,
	}
	cleanupThrottleKeys(t, pool, attempt)

	if err := repository.RecordFailure(ctx, attempt); err != nil {
		t.Fatalf("RecordFailure() error = %v; apply migrations to TEST_DATABASE_URL first", err)
	}
	if blocked, err := repository.BlockedUntil(ctx, attempt); err != nil || !blocked.IsZero() {
		t.Fatalf("BlockedUntil() = %v, %v; want zero", blocked, err)
	}

	created, err := repository.CompleteLogin(ctx, auth.CompleteLoginParams{
		Account:              storedAccount,
		ExpectedPasswordHash: passwordHash,
		Session: session.CreateParams{
			UserID:            account.ID,
			TokenDigest:       bytes.Repeat([]byte{32}, 32),
			CSRFToken:         "integration-csrf-token",
			CreatedAt:         now,
			AbsoluteExpiresAt: now.Add(session.AbsoluteLifetime),
		},
		Attempt: attempt,
	})
	if err != nil {
		t.Fatalf("CompleteLogin() error = %v", err)
	}
	if created.ID == 0 || created.UserID != account.ID {
		t.Errorf("CompleteLogin() session = %+v", created)
	}

	var lastLoginAt *time.Time
	if err := pool.QueryRow(
		ctx,
		"SELECT last_login_at FROM users WHERE id = $1",
		account.ID,
	).Scan(&lastLoginAt); err != nil {
		t.Fatalf("query last_login_at: %v", err)
	}
	if lastLoginAt == nil || !lastLoginAt.Equal(now) {
		t.Errorf("last_login_at = %v, want %v", lastLoginAt, now)
	}

	var loginThrottleCount int
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM login_throttles WHERE key_type = 'login' AND key_digest = $1",
		attempt.LoginKey,
	).Scan(&loginThrottleCount); err != nil {
		t.Fatalf("count login throttle: %v", err)
	}
	if loginThrottleCount != 0 {
		t.Errorf("login throttle count = %d, want 0", loginThrottleCount)
	}

	authenticated := session.AuthenticatedSession{Session: created, User: account}
	if err := repository.Logout(ctx, authenticated, now.Add(time.Minute)); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}

	var revoked bool
	if err := pool.QueryRow(
		ctx,
		"SELECT revoked_at IS NOT NULL FROM sessions WHERE id = $1",
		created.ID,
	).Scan(&revoked); err != nil {
		t.Fatalf("query logout session: %v", err)
	}
	if !revoked {
		t.Error("logout session was not revoked")
	}

	rows, err := pool.Query(
		ctx,
		`SELECT action, result, target_label, details->>'remote_ip'
		 FROM audit_events
		 WHERE target_label = $1 AND action LIKE 'auth.%'
		 ORDER BY id`,
		account.Login,
	)
	if err != nil {
		t.Fatalf("query auth audit events: %v", err)
	}
	defer rows.Close()

	want := []struct {
		action, result, remoteIP string
	}{
		{actionLoginFailed, "failure", "127.0.0.1"},
		{actionLoginSucceeded, "success", "127.0.0.1"},
		{actionLogout, "success", ""},
	}
	var index int
	for rows.Next() {
		var action, result, label string
		var remoteIP *string
		if err := rows.Scan(&action, &result, &label, &remoteIP); err != nil {
			t.Fatalf("scan auth audit event: %v", err)
		}
		if index >= len(want) {
			t.Fatalf("unexpected auth audit action %q", action)
		}
		gotIP := ""
		if remoteIP != nil {
			gotIP = *remoteIP
		}
		if action != want[index].action || result != want[index].result ||
			label != account.Login || gotIP != want[index].remoteIP {
			t.Errorf("audit event = %q %q %q %q", action, result, label, gotIP)
		}
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate auth audit events: %v", err)
	}
	if index != len(want) {
		t.Errorf("audit count = %d, want %d", index, len(want))
	}
}

func TestAuthRepositoryThrottlesAtConfiguredLimits(t *testing.T) {
	pool, ctx := openUserRepositoryTestDatabase(t)
	repository := NewAuthRepository(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	attempt := auth.Attempt{
		LoginKey:   bytes.Repeat([]byte{33}, 32),
		IPKey:      bytes.Repeat([]byte{34}, 32),
		LoginLabel: "throttle-test",
		RemoteIP:   "127.0.0.1",
		OccurredAt: now,
	}
	cleanupThrottleKeys(t, pool, attempt)

	for count := 0; count < auth.LoginFailureLimit; count++ {
		if err := repository.RecordFailure(ctx, attempt); err != nil {
			t.Fatalf("RecordFailure() %d error = %v", count+1, err)
		}
	}

	blockedUntil, err := repository.BlockedUntil(ctx, attempt)
	if err != nil {
		t.Fatalf("BlockedUntil() error = %v", err)
	}
	if !blockedUntil.Equal(now.Add(auth.BlockDuration)) {
		t.Errorf("BlockedUntil() = %v, want %v", blockedUntil, now.Add(auth.BlockDuration))
	}
	if err := repository.RecordThrottled(ctx, attempt); err != nil {
		t.Fatalf("RecordThrottled() error = %v", err)
	}

	var failureCount int
	if err := pool.QueryRow(
		ctx,
		"SELECT failure_count FROM login_throttles WHERE key_type = 'login' AND key_digest = $1",
		attempt.LoginKey,
	).Scan(&failureCount); err != nil {
		t.Fatalf("query login failure count: %v", err)
	}
	if failureCount != auth.LoginFailureLimit {
		t.Errorf("failure count = %d, want %d", failureCount, auth.LoginFailureLimit)
	}

	for count := auth.LoginFailureLimit; count < auth.IPFailureLimit; count++ {
		ipAttempt := attempt
		ipAttempt.LoginKey = bytes.Repeat([]byte{byte(40 + count)}, 32)
		ipAttempt.LoginLabel = fmt.Sprintf("ip-throttle-%d", count)
		cleanupThrottleKeys(t, pool, ipAttempt)
		if err := repository.RecordFailure(ctx, ipAttempt); err != nil {
			t.Fatalf("RecordFailure() IP attempt %d error = %v", count+1, err)
		}
		attempt = ipAttempt
	}

	ipBlockedUntil, err := repository.BlockedUntil(ctx, attempt)
	if err != nil {
		t.Fatalf("BlockedUntil() IP error = %v", err)
	}
	if !ipBlockedUntil.Equal(now.Add(auth.BlockDuration)) {
		t.Errorf("IP BlockedUntil() = %v, want %v", ipBlockedUntil, now.Add(auth.BlockDuration))
	}
}

func TestAuthRepositoryRollsBackWhenAuditFails(t *testing.T) {
	pool, ctx := openUserRepositoryTestDatabase(t)
	account := createSessionTestUser(t, ctx, pool)
	repository := NewAuthRepository(pool)
	cleanupAuthFixtures(t, pool, account.ID)

	auditError := errors.New("audit unavailable")
	repository.writeAudit = func(
		context.Context,
		authDatabase,
		string,
		*user.User,
		string,
		string,
		string,
	) error {
		return auditError
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	attempt := auth.Attempt{
		LoginKey:   bytes.Repeat([]byte{35}, 32),
		IPKey:      bytes.Repeat([]byte{36}, 32),
		LoginLabel: account.Login,
		RemoteIP:   "127.0.0.1",
		OccurredAt: now,
	}
	cleanupThrottleKeys(t, pool, attempt)
	if err := repository.RecordFailure(ctx, attempt); !errors.Is(err, auditError) {
		t.Fatalf("RecordFailure() error = %v, want audit error", err)
	}

	var throttleCount int
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM login_throttles WHERE key_digest IN ($1, $2)",
		attempt.LoginKey,
		attempt.IPKey,
	).Scan(&throttleCount); err != nil {
		t.Fatalf("count rolled back throttles: %v", err)
	}
	if throttleCount != 0 {
		t.Errorf("rolled back throttle count = %d, want 0", throttleCount)
	}

	storedAccount, passwordHash, err := repository.FindByLogin(ctx, account.Login)
	if err != nil {
		t.Fatalf("FindByLogin() error = %v", err)
	}
	_, err = repository.CompleteLogin(ctx, auth.CompleteLoginParams{
		Account:              storedAccount,
		ExpectedPasswordHash: passwordHash,
		Session: session.CreateParams{
			UserID:            account.ID,
			TokenDigest:       bytes.Repeat([]byte{37}, 32),
			CSRFToken:         "rollback-login-csrf",
			CreatedAt:         now,
			AbsoluteExpiresAt: now.Add(session.AbsoluteLifetime),
		},
		Attempt: attempt,
	})
	if !errors.Is(err, auditError) {
		t.Fatalf("CompleteLogin() error = %v, want audit error", err)
	}

	var lastLoginAt *time.Time
	var sessionCount int
	if err := pool.QueryRow(
		ctx,
		"SELECT last_login_at FROM users WHERE id = $1",
		account.ID,
	).Scan(&lastLoginAt); err != nil {
		t.Fatalf("query rolled back last_login_at: %v", err)
	}
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM sessions WHERE user_id = $1",
		account.ID,
	).Scan(&sessionCount); err != nil {
		t.Fatalf("count rolled back login sessions: %v", err)
	}
	if lastLoginAt != nil || sessionCount != 0 {
		t.Errorf("rolled back login: last_login_at=%v sessions=%d", lastLoginAt, sessionCount)
	}

	logoutSession, err := NewSessionRepository(pool).Create(ctx, session.CreateParams{
		UserID:            account.ID,
		TokenDigest:       bytes.Repeat([]byte{38}, 32),
		CSRFToken:         "rollback-logout-csrf",
		CreatedAt:         now,
		AbsoluteExpiresAt: now.Add(session.AbsoluteLifetime),
	})
	if err != nil {
		t.Fatalf("create logout rollback fixture: %v", err)
	}
	err = repository.Logout(
		ctx,
		session.AuthenticatedSession{Session: logoutSession, User: account},
		now.Add(time.Minute),
	)
	if !errors.Is(err, auditError) {
		t.Fatalf("Logout() error = %v, want audit error", err)
	}

	var revoked bool
	if err := pool.QueryRow(
		ctx,
		"SELECT revoked_at IS NOT NULL FROM sessions WHERE id = $1",
		logoutSession.ID,
	).Scan(&revoked); err != nil {
		t.Fatalf("query rolled back logout session: %v", err)
	}
	if revoked {
		t.Error("logout session remained revoked after audit rollback")
	}
}

func cleanupAuthFixtures(t *testing.T, pool *pgxpool.Pool, userID int64) {
	t.Helper()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, err := pool.Exec(
			ctx,
			"DELETE FROM audit_events WHERE actor_user_id = $1 OR target_id = $1",
			userID,
		); err != nil {
			t.Errorf("clean up auth audit events: %v", err)
		}
		if _, err := pool.Exec(ctx, "DELETE FROM sessions WHERE user_id = $1", userID); err != nil {
			t.Errorf("clean up auth sessions: %v", err)
		}
	})
}

func cleanupThrottleKeys(t *testing.T, pool *pgxpool.Pool, attempt auth.Attempt) {
	t.Helper()

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, err := pool.Exec(
			ctx,
			"DELETE FROM login_throttles WHERE key_digest IN ($1, $2)",
			attempt.LoginKey,
			attempt.IPKey,
		); err != nil {
			t.Errorf("clean up login throttles: %v", err)
		}
		if _, err := pool.Exec(
			ctx,
			"DELETE FROM audit_events WHERE target_label = $1 AND action LIKE 'auth.%'",
			attempt.LoginLabel,
		); err != nil {
			t.Errorf("clean up throttle audit events: %v", err)
		}
	})
}
