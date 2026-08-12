package client

import (
	"errors"
	"testing"
)

func TestNormalizePhone(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    Phone
		wantErr error
	}{
		{name: "international Russian", raw: "+7 999 123-45-67", want: "+79991234567"},
		{name: "Russian trunk prefix", raw: "8 (999) 123-45-67", want: "+79991234567"},
		{name: "Russian country code without plus", raw: "7 999 123 45 67", want: "+79991234567"},
		{name: "Russian national number", raw: "999 123-45-67", want: "+79991234567"},
		{name: "explicit international", raw: "+44 20 1234 5678", want: "+442012345678"},
		{name: "empty", raw: " ", wantErr: ErrPhoneRequired},
		{name: "letters", raw: "+7 CALL NOW", wantErr: ErrInvalidPhone},
		{name: "extension", raw: "+7 999 123-45-67, 12", wantErr: ErrInvalidPhone},
		{name: "misplaced plus", raw: "7+9991234567", wantErr: ErrInvalidPhone},
		{name: "Unicode digits", raw: "+７９９９１２３４５６７", wantErr: ErrInvalidPhone},
		{name: "too short international", raw: "+1234567", wantErr: ErrInvalidPhone},
		{name: "too long international", raw: "+1234567890123456", wantErr: ErrInvalidPhone},
		{name: "zero country code", raw: "+0123456789", wantErr: ErrInvalidPhone},
		{name: "unsupported local length", raw: "123456789", wantErr: ErrInvalidPhone},
		{name: "unsupported trunk prefix", raw: "69991234567", wantErr: ErrInvalidPhone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizePhone(tt.raw)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NormalizePhone() error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("NormalizePhone() = %q, want %q", got, tt.want)
			}
			if err == nil && got.String() != string(tt.want) {
				t.Errorf("Phone.String() = %q, want %q", got.String(), tt.want)
			}
		})
	}
}
