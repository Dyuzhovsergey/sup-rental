package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const uniqueClientPhoneConstraint = "clients_phone_unique"

// ClientRepository хранит клиентов в PostgreSQL.
type ClientRepository struct {
	pool *pgxpool.Pool
}

// NewClientRepository создаёт PostgreSQL repository клиентов.
func NewClientRepository(pool *pgxpool.Pool) *ClientRepository {
	return &ClientRepository{pool: pool}
}

// Create сохраняет клиента и возвращает модель с назначенным ID.
// Конфликт нормализованного телефона возвращается как client.ErrPhoneExists.
func (r *ClientRepository) Create(
	ctx context.Context,
	customer client.Client,
) (client.Client, error) {
	const query = `
		INSERT INTO clients (full_name, phone)
		VALUES ($1, $2)
		RETURNING id, full_name, phone
	`

	created, err := r.queryClient(ctx, query, customer.FullName, customer.Phone)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) &&
			postgresError.ConstraintName == uniqueClientPhoneConstraint {
			return client.Client{}, client.ErrPhoneExists
		}
		return client.Client{}, fmt.Errorf("insert client: %w", err)
	}
	return created, nil
}

// Get возвращает клиента по внутреннему идентификатору.
func (r *ClientRepository) Get(ctx context.Context, id int64) (client.Client, error) {
	const query = `
		SELECT id, full_name, phone
		FROM clients
		WHERE id = $1
	`

	customer, err := r.queryClient(ctx, query, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return client.Client{}, client.ErrClientNotFound
	}
	if err != nil {
		return client.Client{}, fmt.Errorf("get client: %w", err)
	}
	return customer, nil
}

// FindByPhone возвращает клиента по нормализованному телефону.
func (r *ClientRepository) FindByPhone(
	ctx context.Context,
	phone client.Phone,
) (client.Client, error) {
	const query = `
		SELECT id, full_name, phone
		FROM clients
		WHERE phone = $1
	`

	customer, err := r.queryClient(ctx, query, phone)
	if errors.Is(err, pgx.ErrNoRows) {
		return client.Client{}, client.ErrClientNotFound
	}
	if err != nil {
		return client.Client{}, fmt.Errorf("find client by phone: %w", err)
	}
	return customer, nil
}

func (r *ClientRepository) queryClient(
	ctx context.Context,
	query string,
	arguments ...any,
) (client.Client, error) {
	var customer client.Client
	if err := r.pool.QueryRow(ctx, query, arguments...).Scan(
		&customer.ID,
		&customer.FullName,
		&customer.Phone,
	); err != nil {
		return client.Client{}, err
	}
	return customer, nil
}
