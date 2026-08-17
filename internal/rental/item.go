package rental

import (
	"errors"
	"strconv"
	"strings"

	"github.com/Dyuzhovsergey/sup-rental/internal/equipment"
)

var (
	// ErrInvalidEquipmentID означает, что позиция не связана с физической
	// единицей оборудования.
	ErrInvalidEquipmentID = errors.New("rental equipment ID must be positive")
	// ErrInvalidInventoryNumber означает, что снимок содержит некорректный или
	// не соответствующий типу и модели инвентарный номер.
	ErrInvalidInventoryNumber = errors.New("invalid rental item inventory number")
	// ErrInvalidEquipmentKind означает, что снимок содержит неизвестный тип.
	ErrInvalidEquipmentKind = errors.New("invalid rental item equipment kind")
	// ErrInvalidItemModelCode означает, что код модели не является каноническим.
	ErrInvalidItemModelCode = errors.New("invalid rental item model code")
	// ErrInvalidItemHourlyRate означает, что часовой тариф позиции неположительный.
	ErrInvalidItemHourlyRate = errors.New("invalid rental item hourly rate")
	// ErrInexactHalfHourRate означает, что тариф нельзя точно разделить на два
	// получасовых слота без округления копеек.
	ErrInexactHalfHourRate = errors.New("rental item hourly rate must be divisible by two")
	// ErrEquipmentAlreadyAdded означает повторное добавление физической единицы.
	ErrEquipmentAlreadyAdded = errors.New("equipment is already added to rental")
)

// Item хранит снимок физической единицы и её тарифа в составе аренды.
// Снимок отделяет историю аренды от будущих изменений модели оборудования.
type Item struct {
	// EquipmentID — внутренний идентификатор физической единицы.
	EquipmentID int64
	// InventoryNumber — инвентарный номер на момент фиксации снимка.
	InventoryNumber string
	// Kind — тип оборудования на момент фиксации снимка.
	Kind equipment.Kind
	// ModelCode — канонический код модели на момент фиксации снимка.
	ModelCode string
	// HourlyRateKopecks — часовой тариф модели в копейках.
	HourlyRateKopecks int64
}

func (i Item) validate() error {
	if i.EquipmentID <= 0 {
		return ErrInvalidEquipmentID
	}
	if !i.Kind.Valid() {
		return ErrInvalidEquipmentKind
	}

	normalizedModelCode, err := equipment.NormalizeModelCode(i.ModelCode)
	if err != nil || normalizedModelCode != i.ModelCode {
		return ErrInvalidItemModelCode
	}
	if !inventoryNumberMatches(i.InventoryNumber, i.Kind, i.ModelCode) {
		return ErrInvalidInventoryNumber
	}
	if i.HourlyRateKopecks <= 0 {
		return ErrInvalidItemHourlyRate
	}
	if i.HourlyRateKopecks%2 != 0 {
		return ErrInexactHalfHourRate
	}

	return nil
}

func inventoryNumberMatches(number string, kind equipment.Kind, modelCode string) bool {
	prefix, err := kind.InventoryPrefix()
	if err != nil {
		return false
	}

	sequenceText, found := strings.CutPrefix(number, prefix+"-"+modelCode+"-")
	if !found || sequenceText == "" {
		return false
	}
	sequence, err := strconv.ParseInt(sequenceText, 10, 64)
	if err != nil || sequence <= 0 {
		return false
	}

	expected, err := equipment.InventoryNumber(kind, modelCode, sequence)
	return err == nil && number == expected
}
