// Package admincli реализует интерактивные команды управления единственным admin.
package admincli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

// Command обозначает поддерживаемую административную CLI-команду.
type Command string

const (
	// CommandCreate создаёт единственного администратора.
	CommandCreate Command = "create"
	// CommandResetPassword заменяет password существующего администратора.
	CommandResetPassword Command = "reset-password"
)

var (
	// ErrUsage означает, что CLI вызван без одной поддерживаемой команды.
	ErrUsage = errors.New("usage: admin create | admin reset-password")
	// ErrPasswordMismatch означает, что повторно введённый password отличается.
	ErrPasswordMismatch = errors.New("passwords do not match")
)

// Service определяет административные сценарии, вызываемые CLI.
type Service interface {
	CreateAdmin(ctx context.Context, login, plainPassword string) (user.User, error)
	ResetAdminPassword(ctx context.Context, plainPassword string) (user.User, error)
}

// Prompter безопасно читает обычные и секретные значения из терминала.
type Prompter interface {
	ReadLine(prompt string) (string, error)
	ReadPassword(prompt string) ([]byte, error)
}

// CLI выполняет административные команды через переданные зависимости.
type CLI struct {
	service  Service
	prompter Prompter
	output   io.Writer
}

// New создаёт административный CLI.
func New(service Service, prompter Prompter, output io.Writer) *CLI {
	return &CLI{service: service, prompter: prompter, output: output}
}

// ParseCommand проверяет, что передана ровно одна поддерживаемая команда.
func ParseCommand(arguments []string) (Command, error) {
	if len(arguments) != 1 {
		return "", ErrUsage
	}

	command := Command(arguments[0])
	if command != CommandCreate && command != CommandResetPassword {
		return "", ErrUsage
	}

	return command, nil
}

// Execute интерактивно выполняет ранее проверенную команду.
func (c *CLI) Execute(ctx context.Context, command Command) error {
	switch command {
	case CommandCreate:
		return c.createAdmin(ctx)
	case CommandResetPassword:
		return c.resetAdminPassword(ctx)
	default:
		return ErrUsage
	}
}

func (c *CLI) createAdmin(ctx context.Context) error {
	login, err := c.prompter.ReadLine("Login: ")
	if err != nil {
		return fmt.Errorf("read admin login: %w", err)
	}

	plainPassword, err := c.readConfirmedPassword("Password: ", "Repeat password: ")
	if err != nil {
		return err
	}

	created, err := c.service.CreateAdmin(ctx, login, plainPassword)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(c.output, "Admin %s created.\n", created.Login); err != nil {
		return fmt.Errorf("write create admin result: %w", err)
	}

	return nil
}

func (c *CLI) resetAdminPassword(ctx context.Context) error {
	plainPassword, err := c.readConfirmedPassword(
		"New password: ",
		"Repeat password: ",
	)
	if err != nil {
		return err
	}

	account, err := c.service.ResetAdminPassword(ctx, plainPassword)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(
		c.output,
		"Password for admin %s updated.\n",
		account.Login,
	); err != nil {
		return fmt.Errorf("write reset admin password result: %w", err)
	}

	return nil
}

func (c *CLI) readConfirmedPassword(firstPrompt, secondPrompt string) (string, error) {
	first, err := c.prompter.ReadPassword(firstPrompt)
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	defer clear(first)

	second, err := c.prompter.ReadPassword(secondPrompt)
	if err != nil {
		return "", fmt.Errorf("repeat password: %w", err)
	}
	defer clear(second)

	if !bytes.Equal(first, second) {
		return "", ErrPasswordMismatch
	}

	return string(first), nil
}
