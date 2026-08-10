package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/Dyuzhovsergey/sup-rental/internal/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	actionAdminCreated         = "admin.created"
	actionAdminPasswordChanged = "admin.password_changed"
)

type adminAuditWriter func(
	ctx context.Context,
	tx pgx.Tx,
	action string,
	target user.User,
) error

// AdminRepository атомарно хранит изменения единственного администратора и
// обязательные audit events в PostgreSQL.
type AdminRepository struct {
	pool       *pgxpool.Pool
	writeAudit adminAuditWriter
}

// NewAdminRepository создаёт PostgreSQL repository административного CLI.
func NewAdminRepository(pool *pgxpool.Pool) *AdminRepository {
	return &AdminRepository{pool: pool, writeAudit: writeAdminAudit}
}

// CreateAdmin одной транзакцией создаёт admin и audit event.
func (r *AdminRepository) CreateAdmin(
	ctx context.Context,
	account user.User,
	passwordHash string,
) (user.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return user.User{}, fmt.Errorf("begin create admin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const query = `
		INSERT INTO users (login, password_hash, role, active, last_login_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, login, role, active, last_login_at
	`

	var created user.User
	err = tx.QueryRow(
		ctx,
		query,
		account.Login,
		passwordHash,
		account.Role,
		account.Active,
		account.LastLoginAt,
	).Scan(
		&created.ID,
		&created.Login,
		&created.Role,
		&created.Active,
		&created.LastLoginAt,
	)
	if err != nil {
		return user.User{}, mapCreateAdminError(err)
	}

	if err := r.writeAudit(ctx, tx, actionAdminCreated, created); err != nil {
		return user.User{}, fmt.Errorf("write create admin audit event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return user.User{}, fmt.Errorf("commit create admin transaction: %w", err)
	}

	return created, nil
}

// ResetAdminPassword одной транзакцией заменяет password hash существующего
// admin и записывает audit event. Login, роль и active не изменяются.
func (r *AdminRepository) ResetAdminPassword(
	ctx context.Context,
	passwordHash string,
) (user.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return user.User{}, fmt.Errorf("begin reset admin password transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const findQuery = `
		SELECT id, login, role, active, last_login_at
		FROM users
		WHERE role = 'admin'
		FOR UPDATE
	`

	var account user.User
	err = tx.QueryRow(ctx, findQuery).Scan(
		&account.ID,
		&account.Login,
		&account.Role,
		&account.Active,
		&account.LastLoginAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return user.User{}, user.ErrUserNotFound
	}
	if err != nil {
		return user.User{}, fmt.Errorf("find admin for password reset: %w", err)
	}

	const updateQuery = `
		UPDATE users
		SET password_hash = $1
		WHERE id = $2
	`
	if _, err := tx.Exec(ctx, updateQuery, passwordHash, account.ID); err != nil {
		return user.User{}, fmt.Errorf("update admin password hash: %w", err)
	}

	if err := r.writeAudit(ctx, tx, actionAdminPasswordChanged, account); err != nil {
		return user.User{}, fmt.Errorf("write reset admin password audit event: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return user.User{}, fmt.Errorf("commit reset admin password transaction: %w", err)
	}

	return account, nil
}

func mapCreateAdminError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.ConstraintName {
		case uniqueUserLoginConstraint:
			return user.ErrLoginExists
		case uniqueAdminConstraint:
			return user.ErrAdminExists
		}
	}

	return fmt.Errorf("insert admin: %w", err)
}

func writeAdminAudit(
	ctx context.Context,
	tx pgx.Tx,
	action string,
	target user.User,
) error {
	const query = `
		INSERT INTO audit_events (
			actor_user_id,
			actor_login,
			actor_role,
			action,
			target_type,
			target_id,
			target_label,
			result,
			details
		)
		VALUES (NULL, NULL, NULL, $1, 'user', $2, $3, 'success', $4::jsonb)
	`

	if _, err := tx.Exec(ctx, query, action, target.ID, target.Login, `{"source":"admin_cli"}`); err != nil {
		return fmt.Errorf("insert admin audit event: %w", err)
	}

	return nil
}
