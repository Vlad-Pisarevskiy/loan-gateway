package application

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Правила раздела 4.2 спеки.
const (
	minAmount    = 3_000_000   // 30 000 ₽
	maxAmount    = 500_000_000 // 5 000 000 ₽
	minTerm      = 6
	maxTerm      = 60
	minAge       = 18
	maxAge       = 75
	maxNameRunes = 200
)

var phoneRe = regexp.MustCompile(`^\+7\d{10}$`)

// FieldError — ошибка в одном поле входа.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError — все ошибки валидации входа.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed: %d invalid field(s)", len(e.Fields))
}

// Validate проверяет вход и возвращает заявку без ID.
// now — момент приёма заявки; возраст считается на эту дату в UTC.
func Validate(in Input, now time.Time) (Application, error) {
	var errs []FieldError
	add := func(field, msg string) {
		errs = append(errs, FieldError{Field: field, Message: msg})
	}

	name := strings.TrimSpace(in.FullName)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxNameRunes {
		add("full_name", fmt.Sprintf("must be 1-%d characters", maxNameRunes))
	} else if strings.ContainsFunc(name, unicode.IsControl) {
		// NUL PostgreSQL в text не принимает: без проверки клиент получил бы 500 вместо 400.
		add("full_name", "must not contain control characters")
	}
	birth, err := time.Parse(time.DateOnly, in.BirthDate)
	if err != nil {
		add("birth_date", "must be a date in YYYY-MM-DD format")
	} else if age := Age(birth, now); age < minAge || age > maxAge {
		add("birth_date", fmt.Sprintf("age must be between %d and %d", minAge, maxAge))
	}
	if !phoneRe.MatchString(in.Phone) {
		add("phone", "must match +7XXXXXXXXXX")
	}
	if in.MonthlyIncome <= 0 {
		add("monthly_income", "must be positive (kopecks)")
	}
	switch in.EmploymentType {
	case EmploymentEmployed, EmploymentSelfEmployed, EmploymentEntrepreneur, EmploymentPensioner:
	default:
		add("employment_type", "must be one of employed, self_employed, entrepreneur, pensioner")
	}
	if in.Amount < minAmount || in.Amount > maxAmount {
		add("amount", fmt.Sprintf("must be between %d and %d (kopecks)", minAmount, maxAmount))
	}
	if in.TermMonths < minTerm || in.TermMonths > maxTerm {
		add("term_months", fmt.Sprintf("must be between %d and %d", minTerm, maxTerm))
	}
	if len(errs) > 0 {
		return Application{}, &ValidationError{Fields: errs}
	}
	return Application{
		FullName:       name,
		BirthDate:      birth,
		Phone:          in.Phone,
		MonthlyIncome:  in.MonthlyIncome,
		EmploymentType: in.EmploymentType,
		Amount:         in.Amount,
		TermMonths:     in.TermMonths,
	}, nil
}

// Age возвращает число полных лет на дату now; обе даты берутся в UTC.
func Age(birth, now time.Time) int {
	birth, now = birth.UTC(), now.UTC()
	age := now.Year() - birth.Year()
	if now.Month() < birth.Month() || (now.Month() == birth.Month() && now.Day() < birth.Day()) {
		age--
	}
	return age
}
