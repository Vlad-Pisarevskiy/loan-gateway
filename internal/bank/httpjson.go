package bank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const maxResponseBytes = 1 << 20

// PostJSON отправляет body как JSON и возвращает статус и тело ответа.
// Сетевые ошибки и таймауты возвращаются как ErrTemporary.
func PostJSON(ctx context.Context, c *http.Client, url string, headers map[string]string, body any) (int, []byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: encode request: %w", ErrPermanent, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: build request: %w", ErrPermanent, err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %w", ErrTemporary, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: read response: %w", ErrTemporary, err)
	}
	return resp.StatusCode, data, nil
}
