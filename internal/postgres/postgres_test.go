package postgres

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestOpenRejectsInvalidConnectionString(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	pool, err := Open(ctx, "://invalid")
	if pool != nil {
		pool.Close()
		t.Fatal("Open() pool is not nil for invalid connection string")
	}

	if err == nil {
		t.Fatal("Open() error = nil, want connection string error")
	}

	if !strings.Contains(err.Error(), "create PostgreSQL pool") {
		t.Errorf("Open() error = %q, want create pool context", err)
	}
}

func TestOpenConnectsToPostgreSQL(t *testing.T) {
	connectionString := os.Getenv("TEST_DATABASE_URL")
	if connectionString == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pool, err := Open(ctx, connectionString)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer pool.Close()
}
