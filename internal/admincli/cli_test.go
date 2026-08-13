package admincli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/Dyuzhovsergey/sup-rental/internal/user"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name      string
		arguments []string
		want      Command
		wantErr   error
	}{
		{name: "create", arguments: []string{"create"}, want: CommandCreate},
		{name: "reset", arguments: []string{"reset-password"}, want: CommandResetPassword},
		{name: "missing", wantErr: ErrUsage},
		{name: "unknown", arguments: []string{"delete"}, wantErr: ErrUsage},
		{name: "extra", arguments: []string{"create", "secret"}, wantErr: ErrUsage},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseCommand(tt.arguments)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ParseCommand() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseCommand() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCLIExecuteCreate(t *testing.T) {
	service := &serviceStub{
		created: user.User{ID: 1, Login: "admin", Role: user.RoleAdmin, Active: true},
	}
	prompter := &prompterStub{
		line:      "  ADMIN  ",
		passwords: [][]byte{[]byte("secret1"), []byte("secret1")},
	}
	var output bytes.Buffer
	cli := New(service, prompter, &output)

	if err := cli.Execute(context.Background(), CommandCreate); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if service.gotLogin != "  ADMIN  " || service.gotPassword != "secret1" {
		t.Errorf("CreateAdmin() got login %q and unexpected password", service.gotLogin)
	}
	if output.String() != "Admin admin created.\n" {
		t.Errorf("output = %q", output.String())
	}
}

func TestCLIExecuteResetPassword(t *testing.T) {
	service := &serviceStub{
		reset: user.User{ID: 1, Login: "admin", Role: user.RoleAdmin, Active: true},
	}
	prompter := &prompterStub{
		passwords: [][]byte{[]byte("new-secret"), []byte("new-secret")},
	}
	var output bytes.Buffer
	cli := New(service, prompter, &output)

	if err := cli.Execute(context.Background(), CommandResetPassword); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if service.gotPassword != "new-secret" {
		t.Error("ResetAdminPassword() did not receive supplied password")
	}
	if output.String() != "Password for admin admin updated.\n" {
		t.Errorf("output = %q", output.String())
	}
}

func TestCLIRejectsPasswordMismatchWithoutLeakingValues(t *testing.T) {
	service := &serviceStub{}
	prompter := &prompterStub{
		line:      "admin",
		passwords: [][]byte{[]byte("first-secret"), []byte("second-secret")},
	}
	var output bytes.Buffer
	cli := New(service, prompter, &output)

	err := cli.Execute(context.Background(), CommandCreate)
	if !errors.Is(err, ErrPasswordMismatch) {
		t.Fatalf("Execute() error = %v, want ErrPasswordMismatch", err)
	}
	if service.createCalled {
		t.Error("CreateAdmin() called for mismatching passwords")
	}
	combined := output.String() + err.Error()
	if strings.Contains(combined, "first-secret") || strings.Contains(combined, "second-secret") {
		t.Error("output or error contains a supplied password")
	}
}

func TestNewTerminalPrompterRejectsNonTerminal(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "stdin")
	if err != nil {
		t.Fatalf("CreateTemp() error = %v", err)
	}
	defer file.Close()

	_, err = NewTerminalPrompter(file, &bytes.Buffer{})
	if !errors.Is(err, ErrTerminalRequired) {
		t.Fatalf("NewTerminalPrompter() error = %v, want ErrTerminalRequired", err)
	}
}

type serviceStub struct {
	created      user.User
	reset        user.User
	err          error
	createCalled bool
	gotLogin     string
	gotPassword  string
}

func (s *serviceStub) CreateAdmin(
	_ context.Context,
	login string,
	plainPassword string,
) (user.User, error) {
	s.createCalled = true
	s.gotLogin = login
	s.gotPassword = plainPassword
	return s.created, s.err
}

func (s *serviceStub) ResetAdminPassword(
	_ context.Context,
	plainPassword string,
) (user.User, error) {
	s.gotPassword = plainPassword
	return s.reset, s.err
}

type prompterStub struct {
	line      string
	lineErr   error
	passwords [][]byte
	index     int
}

func (p *prompterStub) ReadLine(string) (string, error) {
	return p.line, p.lineErr
}

func (p *prompterStub) ReadPassword(string) ([]byte, error) {
	if p.index >= len(p.passwords) {
		return nil, errors.New("no password fixture")
	}
	value := append([]byte(nil), p.passwords[p.index]...)
	p.index++
	return value, nil
}
