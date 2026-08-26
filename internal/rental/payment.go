package rental

import (
	"errors"
	"time"
)

// PaymentKind определяет назначение неизменяемой финансовой записи аренды.
type PaymentKind string

const (
	// PaymentKindBase обозначает полную основную оплату при создании аренды.
	PaymentKindBase PaymentKind = "base"
	// PaymentKindOverdue обозначает доплату за просрочку при возврате.
	PaymentKindOverdue PaymentKind = "overdue"
	// PaymentKindRefund обозначает полный возврат основной оплаты при отмене.
	PaymentKindRefund PaymentKind = "refund"
)

var (
	// ErrInvalidPayment означает повреждённую финансовую запись аренды.
	ErrInvalidPayment = errors.New("invalid rental payment")
	// ErrPaymentNotFound означает отсутствие запрошенной финансовой записи.
	ErrPaymentNotFound = errors.New("rental payment not found")
)

// Payment представляет неизменяемый факт получения или возврата денег.
type Payment struct {
	ID               int64
	RentalID         int64
	Kind             PaymentKind
	AmountKopecks    int64
	OccurredAt       time.Time
	ActorUserID      int64
	RelatedPaymentID *int64
}

// RestorePayment проверяет финансовую запись, загруженную из хранилища.
func RestorePayment(payment Payment) (Payment, error) {
	if payment.ID <= 0 || payment.RentalID <= 0 || payment.AmountKopecks <= 0 ||
		payment.OccurredAt.IsZero() || payment.ActorUserID <= 0 {
		return Payment{}, ErrInvalidPayment
	}
	switch payment.Kind {
	case PaymentKindBase, PaymentKindOverdue:
		if payment.RelatedPaymentID != nil {
			return Payment{}, ErrInvalidPayment
		}
	case PaymentKindRefund:
		if payment.RelatedPaymentID == nil || *payment.RelatedPaymentID <= 0 {
			return Payment{}, ErrInvalidPayment
		}
	default:
		return Payment{}, ErrInvalidPayment
	}
	return payment, nil
}

// PaymentSummary содержит зафиксированные финансовые факты одной аренды.
// Отсутствующая основная оплата означает историческую аренду, созданную до
// появления учёта платежей, а не неоплаченную текущую аренду.
type PaymentSummary struct {
	Base    *Payment
	Overdue *Payment
	Refund  *Payment
}

// HasBase сообщает, зафиксирована ли полная основная оплата.
func (s PaymentSummary) HasBase() bool { return s.Base != nil }

// HasRefund сообщает, зафиксирован ли возврат основной оплаты.
func (s PaymentSummary) HasRefund() bool { return s.Refund != nil }
