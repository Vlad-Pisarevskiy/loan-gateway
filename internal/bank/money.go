package bank

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseRubles переводит рубли строкой ("300000.00", "300000.5", "300000") в копейки без float.
func ParseRubles(s string) (int64, error) {
	return parseFixed2(s)
}

// FormatRubles переводит копейки (≥ 0) в рубли строкой с двумя знаками: 30000050 → "300000.50".
func FormatRubles(kopecks int64) string {
	return fmt.Sprintf("%d.%02d", kopecks/100, kopecks%100)
}

// ParsePercent переводит проценты строкой ("15.90", "15.9", "15") в базисные пункты.
func ParsePercent(s string) (int, error) {
	n, err := parseFixed2(s)
	return int(n), err
}

// FormatPercent переводит базисные пункты в проценты строкой с двумя знаками: 1590 → "15.90".
func FormatPercent(bps int) string {
	return fmt.Sprintf("%d.%02d", bps/100, bps%100)
}

// parseFixed2 разбирает неотрицательное десятичное число с 0–2 знаками после точки
// и возвращает его, умноженное на 100.
func parseFixed2(s string) (int64, error) {
	intPart, frac, hasDot := strings.Cut(s, ".")
	if intPart == "" || len(intPart) > 15 || !allDigits(intPart) {
		return 0, fmt.Errorf("invalid decimal %q", s)
	}
	if hasDot && (frac == "" || len(frac) > 2 || !allDigits(frac)) {
		return 0, fmt.Errorf("invalid decimal %q", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	n, err := strconv.ParseInt(intPart+frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid decimal %q: %w", s, err)
	}
	return n, nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
