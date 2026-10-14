package main

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/eastbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/northbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/httpapi"
)

// adapters — банки, которые умеет шлюз. Новый банк = новый адаптер + строка здесь.
var adapters = map[string]func(baseURL string, c *http.Client) bank.Bank{
	eastbank.Code:  func(u string, c *http.Client) bank.Bank { return eastbank.New(u, c) },
	northbank.Code: func(u string, c *http.Client) bank.Bank { return northbank.New(u, c) },
}

// bankCodes — коды банков с адаптерами, по алфавиту.
func bankCodes() []string {
	return slices.Sorted(maps.Keys(adapters))
}

// buildBanks собирает реестр включённых банков и настройки вебхуков асинхронных банков.
func buildBanks(cfg config.Gateway) (*bank.Registry, map[string]httpapi.WebhookBank, error) {
	client := &http.Client{}
	var banks []bank.Bank
	hooks := map[string]httpapi.WebhookBank{}
	for _, bc := range cfg.Banks {
		newAdapter, ok := adapters[bc.Code]
		if !ok {
			return nil, nil, fmt.Errorf("no adapter for bank %q", bc.Code)
		}
		b := newAdapter(bc.URL, client)
		banks = append(banks, b)
		if h, ok := b.(bank.CallbackHandler); ok {
			if bc.WebhookSecret == "" {
				return nil, nil, fmt.Errorf("BANK_%s_WEBHOOK_SECRET is required: the bank sends decisions via webhook",
					strings.ToUpper(bc.Code))
			}
			hooks[bc.Code] = httpapi.WebhookBank{Handler: h, Secret: []byte(bc.WebhookSecret)}
		}
	}
	reg, err := bank.NewRegistry(banks...)
	if err != nil {
		return nil, nil, err
	}
	return reg, hooks, nil
}

// bankTimeouts — таймауты вызова по коду банка.
func bankTimeouts(cfg config.Gateway) map[string]time.Duration {
	timeouts := make(map[string]time.Duration, len(cfg.Banks))
	for _, b := range cfg.Banks {
		timeouts[b.Code] = b.Timeout
	}
	return timeouts
}
