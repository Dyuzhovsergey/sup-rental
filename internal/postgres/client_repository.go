package postgres

import (
	"context"
	"encoding/json"
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
	actionClientUpdated         = "client.updated"
)

type clientAuditDetails struct {
	BeforeFullName string `json:"before_full_name,omitempty"`
	AfterFullName  string `json:"after_full_name,omitempty"`
	PhoneChanged   bool   `json:"phone_changed,omitempty"`
}

type clientAuditWriter func(
	context.Context,
	pgx.Tx,
	string,
	user.User,
	client.Client,
	clientAuditDetails,
) error

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
	if err := r.writeAudit(ctx, tx, actionClientCreated, actor, created, clientAuditDetails{}); err != nil {
		return client.Client{}, fmt.Errorf("write create client audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return client.Client{}, fmt.Errorf("commit create client transaction: %w", err)
	}
	return created, nil
}

// Update изменяет контактные данные клиента и записывает client.updated в той
// же транзакции. Конфликт нормализованного телефона возвращается как
// client.ErrPhoneExists, а отсутствующий клиент — как client.ErrClientNotFound.
func (r *ClientRepository) Update(
	ctx context.Context,
	actor user.User,
	customer client.Client,
) (client.Client, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return client.Client{}, fmt.Errorf("begin update client transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	const selectQuery = `
		SELECT id, full_name, phone
		FROM clients
		WHERE id = $1
		FOR UPDATE
	`
	before, err := queryClient(ctx, tx, selectQuery, customer.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return client.Client{}, client.ErrClientNotFound
	}
	if err != nil {
		return client.Client{}, fmt.Errorf("lock client for update: %w", err)
	}

	const updateQuery = `
		UPDATE clients
		SET full_name = $1, phone = $2
		WHERE id = $3
		RETURNING id, full_name, phone
	`
	updated, err := queryClient(
		ctx, tx, updateQuery, customer.FullName, customer.Phone, customer.ID,
	)
	if err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) &&
			postgresError.ConstraintName == uniqueClientPhoneConstraint {
			return client.Client{}, client.ErrPhoneExists
		}
		return client.Client{}, fmt.Errorf("update client: %w", err)
	}

	details := clientAuditDetails{
		BeforeFullName: before.FullName,
		AfterFullName:  updated.FullName,
		PhoneChanged:   before.Phone != updated.Phone,
	}
	if err := r.writeAudit(ctx, tx, actionClientUpdated, actor, updated, details); err != nil {
		return client.Client{}, fmt.Errorf("write update client audit event: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return client.Client{}, fmt.Errorf("commit update client transaction: %w", err)
	}
	return updated, nil
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

func writeClientAudit(
	ctx context.Context,
	tx pgx.Tx,
	action string,
	actor user.User,
	target client.Client,
	details clientAuditDetails,
) error {
	encodedDetails, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode client audit details: %w", err)
	}
	const query = `
		INSERT INTO audit_events (
			actor_user_id, actor_login, actor_role, action, target_type,
			target_id, target_label, result, details
		)
		VALUES ($1, $2, $3, $4, 'client', $5, $6, 'success', $7::jsonb)
	`
	if _, err := tx.Exec(
		ctx, query, actor.ID, actor.Login, actor.Role, action,
		target.ID, target.FullName, encodedDetails,
	); err != nil {
		return fmt.Errorf("insert client audit event: %w", err)
	}
	return nil
}
