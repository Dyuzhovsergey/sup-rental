// Command admin предоставляет локальные операции создания и восстановления
// единственного администратора SUP Rental.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/Dyuzhovsergey/sup-rental/internal/admincli"
	"github.com/Dyuzhovsergey/sup-rental/internal/config"
	"github.com/Dyuzhovsergey/sup-rental/internal/password"
	"github.com/Dyuzhovsergey/sup-rental/internal/postgres"
	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "admin:", err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	arguments []string,
	input *os.File,
	output io.Writer,
	promptOutput io.Writer,
) error {
	command, err := admincli.ParseCommand(arguments)
	if err != nil {
		return err
	}

	prompter, err := admincli.NewTerminalPrompter(input, promptOutput)
	if err != nil {
		return fmt.Errorf("initialize terminal: %w", err)
	}

	cfg, err := config.LoadDatabase()
	if err != nil {
		return fmt.Errorf("load database config: %w", err)
	}

	dbCtx, cancelDB := context.WithTimeout(ctx, cfg.DBConnectTimeout)
	pool, err := postgres.Open(dbCtx, cfg.DatabaseURL)
	cancelDB()
	if err != nil {
		return fmt.Errorf("open PostgreSQL: %w", err)
	}
	defer pool.Close()

	repository := postgres.NewAdminRepository(pool)
	service := user.NewAdminService(repository, password.NewHasher())
	cli := admincli.New(service, prompter, output)

	if err := cli.Execute(ctx, command); err != nil {
		return fmt.Errorf("execute %s: %w", command, err)
	}

	return nil
}
