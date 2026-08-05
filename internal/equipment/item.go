// Package equipment содержит доменную модель и бизнес-логику учёта оборудования.
package equipment

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

// CanEditDetails сообщает, можно ли вручную изменить инвентарный номер и тип.
//
// Выданное оборудование не редактируется, чтобы не менять данные предмета во
// время будущей аренды. Списанное оборудование сохраняется неизменным как
// историческая запись.
func (s Status) CanEditDetails() bool {
	return s == StatusAvailable || s == StatusMaintenance
}

// Item представляет отдельный физический предмет инвентаря.
type Item struct {
	// ID — внутренний идентификатор предмета.
	ID int64
	// InventoryNumber — уникальный инвентарный номер, понятный администратору.
	InventoryNumber string
	// Kind — тип предмета.
	Kind Kind
	// Status — текущее физическое состояние предмета.
	Status Status
}
