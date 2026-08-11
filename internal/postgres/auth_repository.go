package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/auth"
	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	actionLoginSucceeded = "auth.login_succeeded"
	actionLoginFailed    = "auth.login_failed"
	actionLoginThrottled = "auth.login_throttled"
	actionLogout         = "auth.logout"
)

// AuthRepository хранит throttling и атомарно объединяет auth-операции с audit.
type AuthRepository struct {
	pool       *pgxpool.Pool
	writeAudit authAuditWriter
}

type authAuditWriter func(
	ctx context.Context,
	database authDatabase,
	action string,
	actor *user.User,
	targetLabel string,
	result string,
	remoteIP string,
) error

// NewAuthRepository создаёт PostgreSQL repository аутентификации.
func NewAuthRepository(pool *pgxpool.Pool) *AuthRepository {
	return &AuthRepository{pool: pool, writeAudit: writeAuthAudit}
}

// FindByLogin возвращает безопасную модель пользователя и отдельный password hash.
func (r *AuthRepository) FindByLogin(
	ctx context.Context,
	normalizedLogin string,
) (user.User, string, error) {
	return NewUserRepository(r.pool).FindByLogin(ctx, normalizedLogin)
}

// BlockedUntil возвращает наиболее позднюю действующую блокировку login или IP.
func (r *AuthRepository) BlockedUntil(
	ctx context.Context,
	attempt auth.Attempt,
) (time.Time, error) {
	return blockedUntil(ctx, r.pool, attempt)
}

// RecordFailure одной транзакцией обновляет login/IP throttling и пишет audit.
func (r *AuthRepository) RecordFailure(
	ctx context.Context,
	attempt auth.Attempt,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin failed login transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := upsertThrottle(
		ctx,
		tx,
		"login",
		attempt.LoginKey,
		auth.LoginFailureLimit,
		attempt.OccurredAt,
	); err != nil {
		return fmt.Errorf("update login throttle: %w", err)
	}
	if err := upsertThrottle(
		ctx,
		tx,
		"ip",
		attempt.IPKey,
		auth.IPFailureLimit,
		attempt.OccurredAt,
	); err != nil {
		return fmt.Errorf("update IP throttle: %w", err)
	}
	if err := r.writeAudit(
		ctx,
		tx,
		actionLoginFailed,
		nil,
		attempt.LoginLabel,
		"failure",
		attempt.RemoteIP,
	); err != nil {
		return fmt.Errorf("write failed login audit: %w", err)
	}

	const cleanupQuery = `
		DELETE FROM login_throttles
		WHERE window_started_at < $1::timestamptz - interval '24 hours'
		  AND (blocked_until IS NULL OR blocked_until <= $1)
	`
	if _, err := tx.Exec(ctx, cleanupQuery, attempt.OccurredAt); err != nil {
		return fmt.Errorf("clean expired login throttles: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit failed login transaction: %w", err)
	}

	return nil
}

// RecordThrottled сохраняет audit event отклонённой заблокированной попытки.
func (r *AuthRepository) RecordThrottled(
	ctx context.Context,
	attempt auth.Attempt,
) error {
	if err := r.writeAudit(
		ctx,
		r.pool,
		actionLoginThrottled,
		nil,
		attempt.LoginLabel,
		"failure",
		attempt.RemoteIP,
	); err != nil {
		return fmt.Errorf("insert throttled login audit: %w", err)
	}

	return nil
}

// CompleteLogin одной транзакцией повторно проверяет пользователя, создаёт
// сессию, обновляет last_login_at, очищает login throttling и пишет audit.
func (r *AuthRepository) CompleteLogin(
	ctx context.Context,
	params auth.CompleteLoginParams,
) (session.Session, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return session.Session{}, fmt.Errorf("begin complete login transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	blocked, err := blockedUntil(ctx, tx, params.Attempt)
	if err != nil {
		return session.Session{}, fmt.Errorf("recheck login throttling: %w", err)
	}
	if blocked.After(params.Attempt.OccurredAt) {
		if err := r.writeAudit(
			ctx,
			tx,
			actionLoginThrottled,
			nil,
			params.Attempt.LoginLabel,
			"failure",
			params.Attempt.RemoteIP,
		); err != nil {
			return session.Session{}, fmt.Errorf("write concurrent throttle audit: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return session.Session{}, fmt.Errorf("commit throttled login audit: %w", err)
		}
		return session.Session{}, &auth.ThrottledError{Until: blocked}
	}

	const lockUserQuery = `
		SELECT login, role, active, last_login_at, password_hash
		FROM users
		WHERE id = $1
		FOR UPDATE
	`
	var current user.User
	var currentHash string
	current.ID = params.Account.ID
	err = tx.QueryRow(ctx, lockUserQuery, params.Account.ID).Scan(
		&current.Login,
		&current.Role,
		&current.Active,
		&current.LastLoginAt,
		&currentHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return session.Session{}, auth.ErrLoginStateChanged
	}
	if err != nil {
		return session.Session{}, fmt.Errorf("lock login user: %w", err)
	}
	if !current.Active || currentHash != params.ExpectedPasswordHash {
		return session.Session{}, auth.ErrLoginStateChanged
	}

	const updateUserQuery = `
		UPDATE users
		SET last_login_at = $2
		WHERE id = $1
	`
	if _, err := tx.Exec(
		ctx,
		updateUserQuery,
		current.ID,
		params.Attempt.OccurredAt,
	); err != nil {
		return session.Session{}, fmt.Errorf("update last successful login: %w", err)
	}

	created, err := createSessionInTransaction(ctx, tx, params.Session)
	if err != nil {
		return session.Session{}, err
	}

	const clearLoginThrottleQuery = `
		DELETE FROM login_throttles
		WHERE key_type = 'login' AND key_digest = $1
	`
	if _, err := tx.Exec(ctx, clearLoginThrottleQuery, params.Attempt.LoginKey); err != nil {
		return session.Session{}, fmt.Errorf("clear login throttle: %w", err)
	}

	current.LastLoginAt = &params.Attempt.OccurredAt
	if err := r.writeAudit(
		ctx,
		tx,
		actionLoginSucceeded,
		&current,
		current.Login,
		"success",
		params.Attempt.RemoteIP,
	); err != nil {
		return session.Session{}, fmt.Errorf("write successful login audit: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return session.Session{}, fmt.Errorf("commit successful login: %w", err)
	}

	return created, nil
}

// Logout одной транзакцией отзывает сессию и пишет audit event пользователя.
func (r *AuthRepository) Logout(
	ctx context.Context,
	authenticated session.AuthenticatedSession,
	occurredAt time.Time,
) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin logout transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const revokeQuery = `
		UPDATE sessions
		SET revoked_at = $2
		WHERE id = $1 AND user_id = $3 AND revoked_at IS NULL
	`
	if _, err := tx.Exec(
		ctx,
		revokeQuery,
		authenticated.Session.ID,
		occurredAt,
		authenticated.User.ID,
	); err != nil {
		return fmt.Errorf("revoke logout session: %w", err)
	}

	if err := r.writeAudit(
		ctx,
		tx,
		actionLogout,
		&authenticated.User,
		authenticated.User.Login,
		"success",
		"",
	); err != nil {
		return fmt.Errorf("write logout audit: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit logout: %w", err)
	}

	return nil
}

type authQueryer interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type authExecer interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
}

type authDatabase interface {
	authQueryer
	authExecer
}

func blockedUntil(
	ctx context.Context,
	database authQueryer,
	attempt auth.Attempt,
) (time.Time, error) {
	const query = `
		SELECT max(blocked_until)
		FROM login_throttles
		WHERE blocked_until > $3
		  AND (
			(key_type = 'login' AND key_digest = $1)
			OR (key_type = 'ip' AND key_digest = $2)
		  )
	`

	var blocked *time.Time
	if err := database.QueryRow(
		ctx,
		query,
		attempt.LoginKey,
		attempt.IPKey,
		attempt.OccurredAt,
	).Scan(&blocked); err != nil {
		return time.Time{}, fmt.Errorf("query login throttles: %w", err)
	}
	if blocked == nil {
		return time.Time{}, nil
	}

	return blocked.UTC(), nil
}

func upsertThrottle(
	ctx context.Context,
	tx pgx.Tx,
	keyType string,
	keyDigest []byte,
	limit int,
	now time.Time,
) error {
	const query = `
		INSERT INTO login_throttles (
			key_type, key_digest, window_started_at, failure_count, blocked_until
		)
		VALUES ($1, $2, $3, 1, NULL)
		ON CONFLICT (key_type, key_digest) DO UPDATE
		SET window_started_at = CASE
				WHEN login_throttles.window_started_at <= $3 - $4::interval THEN $3
				ELSE login_throttles.window_started_at
			END,
			failure_count = CASE
				WHEN login_throttles.window_started_at <= $3 - $4::interval THEN 1
				ELSE login_throttles.failure_count + 1
			END,
			blocked_until = CASE
				WHEN login_throttles.blocked_until > $3 THEN login_throttles.blocked_until
				WHEN login_throttles.window_started_at <= $3 - $4::interval THEN NULL
				WHEN login_throttles.failure_count + 1 >= $5 THEN $3 + $6::interval
				ELSE NULL
			END
	`

	if _, err := tx.Exec(
		ctx,
		query,
		keyType,
		keyDigest,
		now,
		auth.AttemptWindow,
		limit,
		auth.BlockDuration,
	); err != nil {
		return fmt.Errorf("upsert %s throttle: %w", keyType, err)
	}

	return nil
}

func createSessionInTransaction(
	ctx context.Context,
	tx pgx.Tx,
	params session.CreateParams,
) (session.Session, error) {
	const query = `
		INSERT INTO sessions (
			user_id, token_digest, csrf_token, created_at, last_seen_at,
			absolute_expires_at
		)
		VALUES ($1, $2, $3, $4, $4, $5)
		RETURNING id, user_id, csrf_token, created_at, last_seen_at,
		          absolute_expires_at
	`

	created, err := scanSession(tx.QueryRow(
		ctx,
		query,
		params.UserID,
		params.TokenDigest,
		params.CSRFToken,
		params.CreatedAt,
		params.AbsoluteExpiresAt,
	))
	if err != nil {
		return session.Session{}, fmt.Errorf("insert login session: %w", err)
	}

	return created, nil
}

func writeAuthAudit(
	ctx context.Context,
	database authDatabase,
	action string,
	actor *user.User,
	targetLabel string,
	result string,
	remoteIP string,
) error {
	var actorID *int64
	var actorLogin *string
	var actorRole *user.Role
	var targetID *int64
	if actor != nil {
		actorID = &actor.ID
		actorLogin = &actor.Login
		actorRole = &actor.Role
		targetID = &actor.ID
	}

	details := map[string]string{}
	if remoteIP != "" {
		details["remote_ip"] = remoteIP
	}
	encodedDetails, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode auth audit details: %w", err)
	}

	const query = `
		INSERT INTO audit_events (
			actor_user_id, actor_login, actor_role, action, target_type,
			target_id, target_label, result, details
		)
		VALUES ($1, $2, $3, $4, 'user', $5, $6, $7, $8::jsonb)
	`
	if _, err := database.Exec(
		ctx,
		query,
		actorID,
		actorLogin,
		actorRole,
		action,
		targetID,
		targetLabel,
		result,
		string(encodedDetails),
	); err != nil {
		return fmt.Errorf("insert auth audit event: %w", err)
	}

	return nil
}
