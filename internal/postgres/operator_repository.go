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
	actionOperatorCreated         = "operator.created"
	actionOperatorDisabled        = "operator.disabled"
	actionOperatorActivated       = "operator.activated"
	actionOperatorPasswordChanged = "operator.password_changed"
)

type operatorAuditWriter func(context.Context, pgx.Tx, string, user.User, user.User) error

// OperatorRepository хранит операторов и атомарно записывает обязательный
// аудит административных изменений.
type OperatorRepository struct {
	pool       *pgxpool.Pool
	writeAudit operatorAuditWriter
}

// NewOperatorRepository создаёт PostgreSQL repository управления операторами.
func NewOperatorRepository(pool *pgxpool.Pool) *OperatorRepository {
	return &OperatorRepository{pool: pool, writeAudit: writeOperatorAudit}
}

// ListOperators возвращает только учётные записи роли operator.
func (r *OperatorRepository) ListOperators(ctx context.Context) ([]user.User, error) {
	const query = `
		SELECT id, login, role, active, last_login_at
		FROM users
		WHERE role = 'operator'
		ORDER BY login, id
	`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query operators: %w", err)
	}
	defer rows.Close()

	var accounts []user.User
	for rows.Next() {
		account, err := scanOperator(rows)
		if err != nil {
			return nil, fmt.Errorf("scan operator: %w", err)
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate operators: %w", err)
	}
	return accounts, nil
}

// GetOperator возвращает оператора по ID и никогда не возвращает admin.
func (r *OperatorRepository) GetOperator(ctx context.Context, id int64) (user.User, error) {
	const query = `
		SELECT id, login, role, active, last_login_at
		FROM users
		WHERE id = $1 AND role = 'operator'
	`
	account, err := scanOperator(r.pool.QueryRow(ctx, query, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return user.User{}, user.ErrOperatorNotFound
	}
	if err != nil {
		return user.User{}, fmt.Errorf("query operator by ID: %w", err)
	}
	return account, nil
}

// CreateOperator одной транзакцией создаёт operator и audit event от admin.
func (r *OperatorRepository) CreateOperator(
	ctx context.Context,
	actor user.User,
	account user.User,
	passwordHash string,
) (user.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return user.User{}, fmt.Errorf("begin create operator transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const query = `
		INSERT INTO users (login, password_hash, role, active, last_login_at)
		VALUES ($1, $2, 'operator', true, NULL)
		RETURNING id, login, role, active, last_login_at
	`
	created, err := scanOperator(tx.QueryRow(ctx, query, account.Login, passwordHash))
	if err != nil {
		return user.User{}, mapCreateOperatorError(err)
	}
	if err := r.writeAudit(ctx, tx, actionOperatorCreated, actor, created); err != nil {
		return user.User{}, fmt.Errorf("write create operator audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return user.User{}, fmt.Errorf("commit create operator transaction: %w", err)
	}
	return created, nil
}

// SetOperatorActive одной транзакцией изменяет доступ operator, при отключении
// отзывает его сессии и записывает audit event.
func (r *OperatorRepository) SetOperatorActive(
	ctx context.Context,
	actor user.User,
	id int64,
	active bool,
) (user.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return user.User{}, fmt.Errorf("begin set operator active transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	account, err := lockOperator(ctx, tx, id)
	if err != nil {
		return user.User{}, err
	}
	if account.Active == active {
		if active {
			return user.User{}, user.ErrOperatorAlreadyActive
		}
		return user.User{}, user.ErrOperatorAlreadyDisabled
	}

	const updateQuery = `
		UPDATE users
		SET active = $2
		WHERE id = $1
		RETURNING id, login, role, active, last_login_at
	`
	updated, err := scanOperator(tx.QueryRow(ctx, updateQuery, id, active))
	if err != nil {
		return user.User{}, fmt.Errorf("update operator active state: %w", err)
	}

	action := actionOperatorActivated
	if !active {
		if err := revokeOperatorSessions(ctx, tx, id); err != nil {
			return user.User{}, err
		}
		action = actionOperatorDisabled
	}
	if err := r.writeAudit(ctx, tx, action, actor, updated); err != nil {
		return user.User{}, fmt.Errorf("write operator active audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return user.User{}, fmt.Errorf("commit set operator active transaction: %w", err)
	}
	return updated, nil
}

// ChangeOperatorPassword одной транзакцией меняет password hash, отзывает все
// сессии operator и записывает audit event.
func (r *OperatorRepository) ChangeOperatorPassword(
	ctx context.Context,
	actor user.User,
	id int64,
	passwordHash string,
) (user.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return user.User{}, fmt.Errorf("begin change operator password transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	account, err := lockOperator(ctx, tx, id)
	if err != nil {
		return user.User{}, err
	}
	const updateQuery = `UPDATE users SET password_hash = $2 WHERE id = $1`
	if _, err := tx.Exec(ctx, updateQuery, id, passwordHash); err != nil {
		return user.User{}, fmt.Errorf("update operator password hash: %w", err)
	}
	if err := revokeOperatorSessions(ctx, tx, id); err != nil {
		return user.User{}, err
	}
	if err := r.writeAudit(ctx, tx, actionOperatorPasswordChanged, actor, account); err != nil {
		return user.User{}, fmt.Errorf("write operator password audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return user.User{}, fmt.Errorf("commit change operator password transaction: %w", err)
	}
	return account, nil
}

type operatorScanner interface {
	Scan(dest ...any) error
}

func scanOperator(row operatorScanner) (user.User, error) {
	var account user.User
	err := row.Scan(&account.ID, &account.Login, &account.Role, &account.Active, &account.LastLoginAt)
	return account, err
}

func lockOperator(ctx context.Context, tx pgx.Tx, id int64) (user.User, error) {
	const query = `
		SELECT id, login, role, active, last_login_at
		FROM users
		WHERE id = $1 AND role = 'operator'
		FOR UPDATE
	`
	account, err := scanOperator(tx.QueryRow(ctx, query, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return user.User{}, user.ErrOperatorNotFound
	}
	if err != nil {
		return user.User{}, fmt.Errorf("lock operator: %w", err)
	}
	return account, nil
}

func revokeOperatorSessions(ctx context.Context, tx pgx.Tx, id int64) error {
	const query = `
		UPDATE sessions
		SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL
	`
	if _, err := tx.Exec(ctx, query, id); err != nil {
		return fmt.Errorf("revoke operator sessions: %w", err)
	}
	return nil
}

func mapCreateOperatorError(err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && postgresError.ConstraintName == uniqueUserLoginConstraint {
		return user.ErrLoginExists
	}
	return fmt.Errorf("insert operator: %w", err)
}

func writeOperatorAudit(
	ctx context.Context,
	tx pgx.Tx,
	action string,
	actor user.User,
	target user.User,
) error {
	const query = `
		INSERT INTO audit_events (
			actor_user_id, actor_login, actor_role, action, target_type,
			target_id, target_label, result, details
		)
		VALUES ($1, $2, $3, $4, 'user', $5, $6, 'success', '{}'::jsonb)
	`
	if _, err := tx.Exec(
		ctx, query, actor.ID, actor.Login, actor.Role, action, target.ID, target.Login,
	); err != nil {
		return fmt.Errorf("insert operator audit event: %w", err)
	}
	return nil
}
