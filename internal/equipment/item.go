// Package equipment содержит доменную модель и бизнес-логику учёта оборудования.
package equipment

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Kind определяет тип физического предмета инвентаря.
type Kind string

const (
	// KindSUPBoard обозначает SUP-доску.
	KindSUPBoard Kind = "sup_board"
	// KindPaddle обозначает весло.
	KindPaddle Kind = "paddle"
	// KindLifeJacket обозначает спасательный жилет.
	KindLifeJacket Kind = "life_jacket"
)

// Valid сообщает, является ли тип оборудования поддерживаемым.
func (k Kind) Valid() bool {
	switch k {
	case KindSUPBoard, KindPaddle, KindLifeJacket:
		return true
	default:
		return false
	}
}

// InventoryPrefix возвращает латинский префикс инвентарного номера для типа.
func (k Kind) InventoryPrefix() (string, error) {
	switch k {
	case KindSUPBoard:
		return "SUP", nil
	case KindPaddle:
		return "PADDLE", nil
	case KindLifeJacket:
		return "VEST", nil
	default:
		return "", ErrInvalidKind
	}
}

// Status определяет текущее физическое состояние оборудования.
type Status string

const (
	// StatusAvailable обозначает доступное для использования оборудование.
	StatusAvailable Status = "available"
	// StatusIssued обозначает выданное клиенту оборудование.
	StatusIssued Status = "issued"
	// StatusMaintenance обозначает оборудование на обслуживании.
	StatusMaintenance Status = "maintenance"
	// StatusRetired обозначает списанное оборудование.
	StatusRetired Status = "retired"
)

// Valid сообщает, является ли состояние оборудования поддерживаемым.
func (s Status) Valid() bool {
	switch s {
	case StatusAvailable, StatusIssued, StatusMaintenance, StatusRetired:
		return true
	default:
		return false
	}
}

// CanTransitionTo сообщает, разрешён ли ручной переход в target.
//
// До появления аренды вручную можно только направлять оборудование на
// обслуживание, возвращать его из обслуживания и списывать. Статус issued
// изменяется будущими сценариями выдачи и возврата, а retired является конечным.
func (s Status) CanTransitionTo(target Status) bool {
	switch s {
	case StatusAvailable:
		return target == StatusMaintenance || target == StatusRetired
	case StatusMaintenance:
		return target == StatusAvailable || target == StatusRetired
	default:
		return false
	}
}

// CanEditDetails сообщает, можно ли вручную изменить состояние оборудования.
//
// Выданное оборудование не редактируется, чтобы не менять данные предмета во
// время будущей аренды. Списанное оборудование нельзя редактировать; его можно
// только удалить отдельным подтверждённым действием, если оно не связано с
// историческими данными.
func (s Status) CanEditDetails() bool {
	return s == StatusAvailable || s == StatusMaintenance
}

// Item представляет отдельный физический предмет инвентаря.
type Item struct {
	// ID — внутренний идентификатор предмета.
	ID int64
	// InventoryNumber — уникальный инвентарный номер, понятный администратору.
	InventoryNumber string
	// ModelID — внутренний идентификатор модели оборудования.
	ModelID int64
	// ModelCode — нормализованный латинский код модели.
	ModelCode string
	// SequenceNumber — порядковый номер физической единицы внутри модели.
	SequenceNumber int64
	// Kind — тип предмета.
	Kind Kind
	// HourlyRateKopecks — часовой тариф модели в копейках.
	HourlyRateKopecks int64
	// Status — текущее физическое состояние предмета.
	Status Status
}

// BatchCreateInput содержит данные для массового создания физических единиц.
type BatchCreateInput struct {
	// Kind — выбранный тип оборудования.
	Kind Kind
	// ModelCode — введённый код модели до нормализации.
	ModelCode string
	// HourlyRateRubles — положительный целый тариф в рублях за час.
	HourlyRateRubles int64
	// Quantity — количество создаваемых единиц от 1 до 100.
	Quantity int
}

// ModelChangeInput содержит данные целевой модели для переноса физической единицы.
type ModelChangeInput struct {
	// Kind — тип целевой модели.
	Kind Kind
	// ModelCode — код целевой модели до нормализации.
	ModelCode string
	// HourlyRateRubles — тариф целевой модели в целых рублях за час.
	// Для существующей модели значение должно совпадать с её текущим тарифом.
	HourlyRateRubles int64
}

// ModelRateChange описывает результат изменения общего тарифа модели.
type ModelRateChange struct {
	// Item — выбранная физическая единица с обновлённым тарифом модели.
	Item Item
	// AffectedItems — количество единиц, использующих изменённый тариф.
	AffectedItems int
}

// Batch описывает созданную партию оборудования.
type Batch struct {
	// Items содержит каждую созданную физическую единицу.
	Items []Item
	// FirstInventoryNumber — первый номер выделенного диапазона.
	FirstInventoryNumber string
	// LastInventoryNumber — последний номер выделенного диапазона.
	LastInventoryNumber string
}

var (
	// ErrInvalidKind означает, что передан неподдерживаемый тип оборудования.
	ErrInvalidKind = errors.New("invalid equipment kind")
	// ErrModelCodeRequired означает, что код модели не указан.
	ErrModelCodeRequired = errors.New("equipment model code is required")
	// ErrInvalidModelCode означает, что код модели не удалось привести к
	// разрешённому латинскому формату.
	ErrInvalidModelCode = errors.New("invalid equipment model code")
	// ErrInvalidHourlyRate означает неположительный или слишком большой тариф.
	ErrInvalidHourlyRate = errors.New("invalid equipment hourly rate")
	// ErrInvalidBatchQuantity означает количество вне диапазона от 1 до 100.
	ErrInvalidBatchQuantity = errors.New("invalid equipment batch quantity")
	// ErrModelRateConflict означает, что модель уже существует с другим тарифом.
	ErrModelRateConflict = errors.New("equipment model hourly rate conflicts with existing model")
	// ErrEquipmentModelUnchanged означает, что выбрана текущая модель предмета.
	ErrEquipmentModelUnchanged = errors.New("equipment model is unchanged")
	// ErrModelRateUnchanged означает, что введён текущий тариф модели.
	ErrModelRateUnchanged = errors.New("equipment model hourly rate is unchanged")
)

var modelSeparators = regexp.MustCompile(`[\s_\-]+`)
var validModelCode = regexp.MustCompile(`^[A-Z0-9]+(?:-[A-Z0-9]+)*$`)

// NormalizeModelCode приводит пользовательский код модели к каноническому виду.
func NormalizeModelCode(value string) (string, error) {
	value = strings.TrimSpace(strings.ToUpper(value))
	value = strings.Trim(modelSeparators.ReplaceAllString(value, "-"), "-")
	if value == "" {
		return "", ErrModelCodeRequired
	}
	if !validModelCode.MatchString(value) {
		return "", ErrInvalidModelCode
	}
	return value, nil
}

// HourlyRateKopecks преобразует целое количество рублей в копейки без float.
func HourlyRateKopecks(rubles int64) (int64, error) {
	if rubles <= 0 || rubles > math.MaxInt64/100 {
		return 0, ErrInvalidHourlyRate
	}
	return rubles * 100, nil
}

// ValidateBatchQuantity проверяет ограничение размера одной партии.
func ValidateBatchQuantity(quantity int) error {
	if quantity < 1 || quantity > 100 {
		return ErrInvalidBatchQuantity
	}
	return nil
}

// InventoryNumber формирует номер физической единицы из модели и последовательности.
func InventoryNumber(kind Kind, modelCode string, sequenceNumber int64) (string, error) {
	prefix, err := kind.InventoryPrefix()
	if err != nil {
		return "", err
	}
	code, err := NormalizeModelCode(modelCode)
	if err != nil {
		return "", err
	}
	if sequenceNumber <= 0 {
		return "", fmt.Errorf("sequence number must be positive")
	}
	return fmt.Sprintf("%s-%s-%d", prefix, code, sequenceNumber), nil
}

// ListScope определяет группу оборудования для постраничного списка.
type ListScope string

const (
	// ListScopeActive включает всё оборудование, кроме списанного.
	ListScopeActive ListScope = "active"
	// ListScopeRetired включает только списанное оборудование.
	ListScopeRetired ListScope = "retired"
)

// ListPageInput задаёт группу, номер страницы и количество строк.
type ListPageInput struct {
	// Scope выбирает действующее или списанное оборудование.
	Scope ListScope
	// Page задаёт номер страницы начиная с единицы.
	Page int
	// PageSize задаёт разрешённое количество строк.
	PageSize int
}

// ListPage содержит одну страницу оборудования и общее количество записей.
type ListPage struct {
	// Scope повторяет группу выполненной выборки.
	Scope ListScope
	// Items содержит оборудование текущей страницы.
	Items []Item
	// Total — общее количество предметов в группе.
	Total int
	// Page — номер текущей страницы.
	Page int
	// PageSize — максимальное количество строк на странице.
	PageSize int
}
