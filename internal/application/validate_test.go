package application

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func validInput() Input {
	return Input{
		FullName:       "Иванов Иван Иванович",
		BirthDate:      "1990-05-17",
		Phone:          "+79991234567",
		MonthlyIncome:  12_000_000,
		EmploymentType: EmploymentEmployed,
		Amount:         30_000_000,
		TermMonths:     24,
	}
}

func TestValidateAcceptsValidInput(t *testing.T) {
	in := validInput()
	in.FullName = "  Иванов Иван Иванович  "
	app, err := Validate(in, testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !app.BirthDate.Equal(time.Date(1990, 5, 17, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("birth date = %v", app.BirthDate)
	}
	app.BirthDate = time.Time{}
	want := Application{
		FullName:       "Иванов Иван Иванович",
		Phone:          "+79991234567",
		MonthlyIncome:  12_000_000,
		EmploymentType: EmploymentEmployed,
		Amount:         30_000_000,
		TermMonths:     24,
	}
	if app != want {
		t.Fatalf("got %+v, want %+v", app, want)
	}
}

func TestValidateRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*Input)
		field string
	}{
		{"blank name", func(in *Input) { in.FullName = "   " }, "full_name"},
		{"bad birth date format", func(in *Input) { in.BirthDate = "17.05.1990" }, "birth_date"},
		{"turns 18 tomorrow", func(in *Input) { in.BirthDate = "2008-10-14" }, "birth_date"},
		{"turned 76 today", func(in *Input) { in.BirthDate = "1950-10-13" }, "birth_date"},
		{"phone without +7", func(in *Input) { in.Phone = "89991234567" }, "phone"},
		{"phone too short", func(in *Input) { in.Phone = "+7999123456" }, "phone"},
		{"zero income", func(in *Input) { in.MonthlyIncome = 0 }, "monthly_income"},
		{"unknown employment", func(in *Input) { in.EmploymentType = "student" }, "employment_type"},
		{"amount in rubles instead of kopecks", func(in *Input) { in.Amount = 300_000 }, "amount"},
		{"amount above limit", func(in *Input) { in.Amount = 500_000_001 }, "amount"},
		{"term too short", func(in *Input) { in.TermMonths = 5 }, "term_months"},
		{"term too long", func(in *Input) { in.TermMonths = 61 }, "term_months"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validInput()
			tt.edit(&in)
			_, err := Validate(in, testNow)
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("want *ValidationError, got %v", err)
			}
			if len(verr.Fields) != 1 || verr.Fields[0].Field != tt.field {
				t.Fatalf("want one error for %q, got %+v", tt.field, verr.Fields)
			}
		})
	}
}

func TestValidateReportsAllInvalidFields(t *testing.T) {
	_, err := Validate(Input{}, testNow)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want *ValidationError, got %v", err)
	}
	want := []string{"full_name", "birth_date", "phone", "monthly_income", "employment_type", "amount", "term_months"}
	if len(verr.Fields) != len(want) {
		t.Fatalf("got %d errors %+v, want %d", len(verr.Fields), verr.Fields, len(want))
	}
	for i, f := range verr.Fields {
		if f.Field != want[i] {
			t.Errorf("error %d is for %q, want %q", i, f.Field, want[i])
		}
	}
}

func TestValidateAgeBoundaries(t *testing.T) {
	for _, birth := range []string{"2008-10-13", "1950-10-14"} { // ровно 18 и ровно 75
		in := validInput()
		in.BirthDate = birth
		if _, err := Validate(in, testNow); err != nil {
			t.Errorf("birth date %s: unexpected error %v", birth, err)
		}
	}
}

func TestValidateCountsRunes(t *testing.T) {
	in := validInput()
	in.FullName = strings.Repeat("я", 200)
	if _, err := Validate(in, testNow); err != nil {
		t.Fatalf("200 cyrillic letters must be accepted: %v", err)
	}
	in.FullName = strings.Repeat("я", 201)
	if _, err := Validate(in, testNow); err == nil {
		t.Fatal("201 cyrillic letters must be rejected")
	}
}

func TestAge(t *testing.T) {
	tests := []struct {
		birth, now string
		want       int
	}{
		{"1990-05-17", "2026-05-16", 35},
		{"1990-05-17", "2026-05-17", 36},
		{"2000-02-29", "2026-02-28", 25},
		{"2000-02-29", "2026-03-01", 26},
	}
	for _, tt := range tests {
		birth, _ := time.Parse(time.DateOnly, tt.birth)
		now, _ := time.Parse(time.DateOnly, tt.now)
		if got := Age(birth, now); got != tt.want {
			t.Errorf("Age(%s, %s) = %d, want %d", tt.birth, tt.now, got, tt.want)
		}
	}
}
