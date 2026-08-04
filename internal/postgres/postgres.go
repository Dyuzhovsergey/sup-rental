// Package postgres предоставляет подключение к PostgreSQL.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open создаёт connection pool и проверяет доступность PostgreSQL.
//
// При ошибке проверки Open закрывает созданный pool и возвращает ошибку
// с контекстом неуспешной операции.
func Open(ctx context.Context, connectionString string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, connectionString)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}

	return pool, nil
}
