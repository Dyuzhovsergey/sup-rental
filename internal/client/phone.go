package client

import "strings"

const (
	minimumInternationalDigits = 8
	maximumInternationalDigits = 15
	russianNationalDigits      = 10
)

// Phone содержит проверенный телефон в каноническом формате: знак `+`, код
// страны и цифры без разделителей.
type Phone string

// NormalizePhone преобразует распространённые российские формы и явно
// международный номер в каноническую строку.
//
// Номер из десяти цифр считается российским. Функция проверяет только формат и
// не подтверждает существование номера или его назначение абоненту.
func NormalizePhone(raw string) (Phone, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", ErrPhoneRequired
	}

	var digits strings.Builder
	digits.Grow(len(value))
	explicitInternational := false

	for index, symbol := range value {
		switch {
		case symbol >= '0' && symbol <= '9':
			digits.WriteRune(symbol)
		case symbol == '+' && index == 0:
			explicitInternational = true
		case symbol == ' ' || symbol == '(' || symbol == ')' || symbol == '-':
			// Разделители влияют только на отображение введённого номера.
		default:
			return "", ErrInvalidPhone
		}
	}

	digitString := digits.String()
	if explicitInternational {
		if !validInternationalDigits(digitString) {
			return "", ErrInvalidPhone
		}
		return Phone("+" + digitString), nil
	}

	switch {
	case len(digitString) == russianNationalDigits:
		return Phone("+7" + digitString), nil
	case len(digitString) == russianNationalDigits+1 && digitString[0] == '8':
		return Phone("+7" + digitString[1:]), nil
	case len(digitString) == russianNationalDigits+1 && digitString[0] == '7':
		return Phone("+" + digitString), nil
	default:
		return "", ErrInvalidPhone
	}
}

// String возвращает каноническое строковое представление телефона.
func (p Phone) String() string {
	return string(p)
}

func validInternationalDigits(value string) bool {
	return len(value) >= minimumInternationalDigits &&
		len(value) <= maximumInternationalDigits &&
		value[0] >= '1' && value[0] <= '9'
}
