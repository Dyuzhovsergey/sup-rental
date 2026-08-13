package user

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeLogin(t *testing.T) {
	tests := []struct {
		name    string
		login   string
		want    string
		wantErr error
	}{
		{name: "normalizes login", login: "  Operator.One  ", want: "operator.one"},
		{name: "allows separators", login: "sup_operator-1", want: "sup_operator-1"},
		{name: "requires login", login: "   ", wantErr: ErrLoginRequired},
		{name: "rejects short login", login: "ab", wantErr: ErrLoginTooShort},
		{
			name:    "rejects long login",
			login:   strings.Repeat("a", MaxLoginLength+1),
			wantErr: ErrLoginTooLong,
		},
		{name: "rejects separator first", login: "_operator", wantErr: ErrInvalidLogin},
		{name: "rejects whitespace inside", login: "sup operator", wantErr: ErrInvalidLogin},
		{name: "rejects non ASCII", login: "оператор", wantErr: ErrInvalidLogin},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeLogin(tt.login)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NormalizeLogin() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("NormalizeLogin() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNew(t *testing.T) {
	created, err := New("  ADMIN  ", RoleAdmin)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if created.Login != "admin" {
		t.Errorf("Login = %q, want admin", created.Login)
	}
	if created.Role != RoleAdmin {
		t.Errorf("Role = %q, want %q", created.Role, RoleAdmin)
	}
	if !created.Active {
		t.Error("Active = false, want true")
	}
	if created.LastLoginAt != nil {
		t.Errorf("LastLoginAt = %v, want nil", created.LastLoginAt)
	}
}

func TestNewRejectsInvalidRole(t *testing.T) {
	_, err := New("operator", Role("owner"))
	if !errors.Is(err, ErrInvalidRole) {
		t.Fatalf("New() error = %v, want %v", err, ErrInvalidRole)
	}
}

func TestRoleValid(t *testing.T) {
	tests := []struct {
		role Role
		want bool
	}{
		{role: RoleAdmin, want: true},
		{role: RoleOperator, want: true},
		{role: Role("owner"), want: false},
		{role: "", want: false},
	}

	for _, tt := range tests {
		if got := tt.role.Valid(); got != tt.want {
			t.Errorf("Role(%q).Valid() = %t, want %t", tt.role, got, tt.want)
		}
	}
}
