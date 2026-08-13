package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Dyuzhovsergey/sup-rental/internal/session"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SessionRepository хранит server-side сессии в PostgreSQL.
type SessionRepository struct {
	pool *pgxpool.Pool
}

// NewSessionRepository создаёт PostgreSQL repository пользовательских сессий.
func NewSessionRepository(pool *pgxpool.Pool) *SessionRepository {
	return &SessionRepository{pool: pool}
}

// Create сохраняет новую сессию только для существующего активного пользователя.
func (r *SessionRepository) Create(
	ctx context.Context,
	params session.CreateParams,
) (session.Session, error) {
	const query = `
		INSERT INTO sessions (
			user_id,
			token_digest,
			csrf_token,
			created_at,
			last_seen_at,
			absolute_expires_at
		)
		SELECT id, $2, $3, $4, $4, $5
		FROM users
		WHERE id = $1 AND active
		RETURNING id, user_id, csrf_token, created_at, last_seen_at,
		          absolute_expires_at
	`

	created, err := scanSession(r.pool.QueryRow(
		ctx,
		query,
		params.UserID,
		params.TokenDigest,
		params.CSRFToken,
		params.CreatedAt,
		params.AbsoluteExpiresAt,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return session.Session{}, session.ErrUserUnavailable
	}
	if err != nil {
		return session.Session{}, fmt.Errorf("insert session: %w", err)
	}

	return created, nil
}

// Resolve атомарно проверяет срок и владельца сессии, обновляет last_seen_at и
// возвращает актуальную роль пользователя. Недействительные сессии не
// различаются по внешней ошибке.
func (r *SessionRepository) Resolve(
	ctx context.Context,
	tokenDigest []byte,
	now time.Time,
	idleTimeout time.Duration,
) (session.AuthenticatedSession, error) {
	const query = `
		UPDATE sessions AS s
		SET last_seen_at = $2
		FROM users AS u
		WHERE s.token_digest = $1
		  AND u.id = s.user_id
		  AND u.active
		  AND s.revoked_at IS NULL
		  AND s.absolute_expires_at > $2
		  AND s.last_seen_at > $2 - $3::interval
		RETURNING s.id, s.user_id, s.csrf_token, s.created_at,
		          s.last_seen_at, s.absolute_expires_at,
		          u.id, u.login, u.role, u.active, u.last_login_at
	`

	var resolved session.AuthenticatedSession
	err := r.pool.QueryRow(ctx, query, tokenDigest, now, idleTimeout).Scan(
		&resolved.Session.ID,
		&resolved.Session.UserID,
		&resolved.Session.CSRFToken,
		&resolved.Session.CreatedAt,
		&resolved.Session.LastSeenAt,
		&resolved.Session.AbsoluteExpiresAt,
		&resolved.User.ID,
		&resolved.User.Login,
		&resolved.User.Role,
		&resolved.User.Active,
		&resolved.User.LastLoginAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return session.AuthenticatedSession{}, session.ErrSessionNotFound
	}
	if err != nil {
		return session.AuthenticatedSession{}, fmt.Errorf("resolve session: %w", err)
	}

	return resolved, nil
}

// Revoke отмечает соответствующую token сессию отозванной. Повторный отзыв и
// неизвестный token не раскрывают существование сессии и завершаются успешно.
func (r *SessionRepository) Revoke(
	ctx context.Context,
	tokenDigest []byte,
	revokedAt time.Time,
) error {
	const query = `
		UPDATE sessions
		SET revoked_at = $2
		WHERE token_digest = $1 AND revoked_at IS NULL
	`

	if _, err := r.pool.Exec(ctx, query, tokenDigest, revokedAt); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}

	return nil
}

// RevokeAll отмечает отозванными все действующие сессии пользователя.
func (r *SessionRepository) RevokeAll(
	ctx context.Context,
	userID int64,
	revokedAt time.Time,
) error {
	const query = `
		UPDATE sessions
		SET revoked_at = $2
		WHERE user_id = $1 AND revoked_at IS NULL
	`

	if _, err := r.pool.Exec(ctx, query, userID, revokedAt); err != nil {
		return fmt.Errorf("revoke all user sessions: %w", err)
	}

	return nil
}

type rowScanner interface {
	Scan(destinations ...any) error
}

func scanSession(row rowScanner) (session.Session, error) {
	var stored session.Session
	err := row.Scan(
		&stored.ID,
		&stored.UserID,
		&stored.CSRFToken,
		&stored.CreatedAt,
		&stored.LastSeenAt,
		&stored.AbsoluteExpiresAt,
	)
	return stored, err
}
