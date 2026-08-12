package client

import (
	"errors"
	"strings"
	"testing"
)

func TestNewNormalizesClientData(t *testing.T) {
	client, err := New("  Анна   Сергеевна\u00a0Иванова  ", "8 (999) 123-45-67")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if client.ID != 0 {
		t.Errorf("New() ID = %d, want 0", client.ID)
	}
	if client.FullName != "Анна Сергеевна Иванова" {
		t.Errorf("New() FullName = %q", client.FullName)
	}
	if client.Phone != Phone("+79991234567") {
		t.Errorf("New() Phone = %q", client.Phone)
	}
}

func TestNewRejectsInvalidFullName(t *testing.T) {
	tests := []struct {
		name     string
		fullName string
		wantErr  error
	}{
		{name: "empty", fullName: "  ", wantErr: ErrFullNameRequired},
		{name: "control character", fullName: "Анна\nИванова", wantErr: ErrInvalidFullName},
		{name: "invalid UTF-8", fullName: string([]byte{0xff}), wantErr: ErrInvalidFullName},
		{name: "too long", fullName: strings.Repeat("Я", maxFullNameLength+1), wantErr: ErrFullNameTooLong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.fullName, "+79991234567")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("New() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewPreservesPhoneError(t *testing.T) {
	_, err := New("Анна Иванова", "not-a-phone")
	if !errors.Is(err, ErrInvalidPhone) {
		t.Fatalf("New() error = %v, want ErrInvalidPhone", err)
	}
}
