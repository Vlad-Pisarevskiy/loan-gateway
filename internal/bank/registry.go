package bank

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

// Registry — включённые банки по коду.
type Registry struct {
	banks map[string]Bank
}

// NewRegistry проверяет, что банков хотя бы один и коды не повторяются.
func NewRegistry(banks ...Bank) (*Registry, error) {
	if len(banks) == 0 {
		return nil, errors.New("no banks configured: set BANK_<CODE>_URL")
	}
	r := &Registry{banks: make(map[string]Bank, len(banks))}
	for _, b := range banks {
		if _, dup := r.banks[b.Code()]; dup {
			return nil, fmt.Errorf("duplicate bank code %q", b.Code())
		}
		r.banks[b.Code()] = b
	}
	return r, nil
}

// Get возвращает адаптер банка по коду.
func (r *Registry) Get(code string) (Bank, bool) {
	b, ok := r.banks[code]
	return b, ok
}

// Codes возвращает коды банков по алфавиту.
func (r *Registry) Codes() []string {
	return slices.Sorted(maps.Keys(r.banks))
}
