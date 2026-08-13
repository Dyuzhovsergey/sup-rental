package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/Dyuzhovsergey/sup-rental/internal/client"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	uniqueClientPhoneConstraint = "clients_phone_unique"
	actionClientCreated         = "client.created"
)

type clientAuditWriter func(context.Context, pgx.Tx, string, user.User, client.Client) error

// ClientRepository хранит клиентов в PostgreSQL.
type ClientRepository struct {
	pool       *pgxpool.Pool
	writeAudit clientAuditWriter
}

// NewClientRepository создаёт PostgreSQL repository клиентов.
func NewClientRepository(pool *pgxpool.Pool) *ClientRepository {
	return &ClientRepository{pool: pool, writeAudit: writeClientAudit}
}

// Create сохраняет клиента и возвращает модель с назначенным ID.
// Конфликт нормализованного телефона возвращается как client.ErrPhoneExists.
func (r *ClientRepository) Create(
	ctx context.Context,
	actor user.User,
	customer client.Client,
) (client.Client, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return client.Client{}, fmt.Errorf("begin create client transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const query = `
		INSERT INTO clients (full_name, phone)
		VALUES ($1, $2)
		RETURNING id, full_name, phone
	`

	created, err := queryClient(ctx, tx, query, customer.FullName, customer.Phone)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) &&
			postgresError.ConstraintName == uniqueClientPhoneConstraint {
			return client.Client{}, client.ErrPhoneExists
		}
		return client.Client{}, fmt.Errorf("insert client: %w", err)
	}
	if err := r.writeAudit(ctx, tx, actionClientCreated, actor, created); err != nil {
		return client.Client{}, fmt.Errorf("write create client audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return client.Client{}, fmt.Errorf("commit create client transaction: %w", err)
	}
	return created, nil
}

// ListPage возвращает страницу клиентов от новых записей к старым.
func (r *ClientRepository) ListPage(ctx context.Context, page, pageSize int) (client.Page, error) {
	var total int
	if err := r.pool.QueryRow(ctx, "SELECT count(*) FROM clients").Scan(&total); err != nil {
		return client.Page{}, fmt.Errorf("count clients: %w", err)
	}
	const query = `
		SELECT id, full_name, phone
		FROM clients
		ORDER BY id DESC
		LIMIT $1 OFFSET $2
	`
	rows, err := r.pool.Query(ctx, query, pageSize, (page-1)*pageSize)
	if err != nil {
		return client.Page{}, fmt.Errorf("query clients page: %w", err)
	}
	defer rows.Close()

	customers := make([]client.Client, 0, pageSize)
	for rows.Next() {
		var customer client.Client
		if err := rows.Scan(&customer.ID, &customer.FullName, &customer.Phone); err != nil {
			return client.Page{}, fmt.Errorf("scan client page: %w", err)
		}
		customers = append(customers, customer)
	}
	if err := rows.Err(); err != nil {
		return client.Page{}, fmt.Errorf("iterate client page: %w", err)
	}
	return client.Page{Clients: customers, Total: total, Page: page}, nil
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
	return queryClient(ctx, r.pool, query, arguments...)
}

type clientQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func queryClient(ctx context.Context, database clientQueryer, query string, arguments ...any) (client.Client, error) {
	var customer client.Client
	if err := database.QueryRow(ctx, query, arguments...).Scan(
		&customer.ID,
		&customer.FullName,
		&customer.Phone,
	); err != nil {
		return client.Client{}, err
	}
	return customer, nil
}

func writeClientAudit(ctx context.Context, tx pgx.Tx, action string, actor user.User, target client.Client) error {
	const query = `
		INSERT INTO audit_events (
			actor_user_id, actor_login, actor_role, action, target_type,
			target_id, target_label, result, details
		)
		VALUES ($1, $2, $3, $4, 'client', $5, $6, 'success', '{}'::jsonb)
	`
	if _, err := tx.Exec(ctx, query, actor.ID, actor.Login, actor.Role, action, target.ID, target.FullName); err != nil {
		return fmt.Errorf("insert client audit event: %w", err)
	}
	return nil
}
