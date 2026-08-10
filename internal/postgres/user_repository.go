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
	uniqueUserLoginConstraint = "users_login_unique"
	uniqueAdminConstraint     = "users_single_admin_idx"
)

// UserRepository хранит учётные записи пользователей в PostgreSQL.
type UserRepository struct {
	pool *pgxpool.Pool
}

// NewUserRepository создаёт PostgreSQL repository пользователей.
func NewUserRepository(pool *pgxpool.Pool) *UserRepository {
	return &UserRepository{pool: pool}
}

// Create сохраняет пользователя и готовый password hash, возвращая безопасную
// модель без password hash с назначенным ID.
func (r *UserRepository) Create(
	ctx context.Context,
	account user.User,
	passwordHash string,
) (user.User, error) {
	const query = `
		INSERT INTO users (login, password_hash, role, active, last_login_at)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, login, role, active, last_login_at
	`

	created, err := r.queryUser(
		ctx,
		query,
		account.Login,
		passwordHash,
		account.Role,
		account.Active,
		account.LastLoginAt,
	)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) {
			switch postgresError.ConstraintName {
			case uniqueUserLoginConstraint:
				return user.User{}, user.ErrLoginExists
			case uniqueAdminConstraint:
				return user.User{}, user.ErrAdminExists
			}
		}

		return user.User{}, fmt.Errorf("insert user: %w", err)
	}

	return created, nil
}

// FindByLogin возвращает пользователя и его password hash по нормализованному
// login. Hash предназначен только для будущей проверки password и не входит в
// возвращаемую доменную модель.
func (r *UserRepository) FindByLogin(
	ctx context.Context,
	normalizedLogin string,
) (user.User, string, error) {
	const query = `
		SELECT id, login, role, active, last_login_at, password_hash
		FROM users
		WHERE login = $1
	`

	var account user.User
	var passwordHash string
	err := r.pool.QueryRow(ctx, query, normalizedLogin).Scan(
		&account.ID,
		&account.Login,
		&account.Role,
		&account.Active,
		&account.LastLoginAt,
		&passwordHash,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return user.User{}, "", user.ErrUserNotFound
	}
	if err != nil {
		return user.User{}, "", fmt.Errorf("find user by login: %w", err)
	}

	return account, passwordHash, nil
}

func (r *UserRepository) queryUser(
	ctx context.Context,
	query string,
	arguments ...any,
) (user.User, error) {
	var account user.User
	err := r.pool.QueryRow(ctx, query, arguments...).Scan(
		&account.ID,
		&account.Login,
		&account.Role,
		&account.Active,
		&account.LastLoginAt,
	)
	if err != nil {
		return user.User{}, err
	}

	return account, nil
}
