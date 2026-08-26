package rental

import (
	"errors"
	"testing"
	"time"
)

func TestRestorePayment(t *testing.T) {
	now := time.Now()
	baseID := int64(4)
	tests := []struct {
		name    string
		payment Payment
		wantErr bool
	}{
		{name: "base", payment: Payment{ID: 1, RentalID: 2, Kind: PaymentKindBase, AmountKopecks: 100, OccurredAt: now, ActorUserID: 3}},
		{name: "refund", payment: Payment{ID: 5, RentalID: 2, Kind: PaymentKindRefund, AmountKopecks: 100, OccurredAt: now, ActorUserID: 3, RelatedPaymentID: &baseID}},
		{name: "zero amount", payment: Payment{ID: 1, RentalID: 2, Kind: PaymentKindBase, OccurredAt: now, ActorUserID: 3}, wantErr: true},
		{name: "refund without relation", payment: Payment{ID: 1, RentalID: 2, Kind: PaymentKindRefund, AmountKopecks: 100, OccurredAt: now, ActorUserID: 3}, wantErr: true},
		{name: "unknown kind", payment: Payment{ID: 1, RentalID: 2, Kind: "cash", AmountKopecks: 100, OccurredAt: now, ActorUserID: 3}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := RestorePayment(tt.payment)
			if (err != nil) != tt.wantErr || tt.wantErr && !errors.Is(err, ErrInvalidPayment) {
				t.Fatalf("RestorePayment() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
