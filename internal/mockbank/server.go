package mockbank

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// Options — поведение заглушки, общее для всех диалектов (раздел 6.5 спеки).
type Options struct {
	Latency   time.Duration    // задержка перед каждым ответом
	FailFirst int              // первые N запросов с каждым ключом идемпотентности получают 503
	Now       func() time.Time // по умолчанию time.Now
}

type savedResponse struct {
	status int
	body   []byte
}

// server — общая для диалектов механика: задержка, искусственные сбои, идемпотентность.
// Состояние хранится в памяти.
type server struct {
	opts     Options
	mu       sync.Mutex
	failures map[string]int
	saved    map[string]savedResponse
}

func newServer(opts Options) *server {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &server{opts: opts, failures: map[string]int{}, saved: map[string]savedResponse{}}
}

type errorResponse struct {
	Error string `json:"error"`
}

// handle обслуживает запрос диалекта. produce получает тело и возвращает статус и ответ.
// Успешный (2xx) ответ сохраняется и отдаётся на повторы с тем же ключом; искусственные
// 503 и ошибки не сохраняются. produce вызывается под мьютексом.
func (s *server) handle(w http.ResponseWriter, r *http.Request, key string, produce func(body []byte) (int, any)) {
	if s.opts.Latency > 0 {
		select {
		case <-time.After(s.opts.Latency):
		case <-r.Context().Done():
			return
		}
	}
	if key == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "missing idempotency key"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "cannot read body"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if saved, ok := s.saved[key]; ok {
		writeRaw(w, saved.status, saved.body)
		return
	}
	if s.failures[key] < s.opts.FailFirst {
		s.failures[key]++
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "temporarily unavailable"})
		return
	}
	status, v := produce(body)
	data, err := json.Marshal(v)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "cannot encode response"})
		return
	}
	if status >= 200 && status < 300 {
		s.saved[key] = savedResponse{status: status, body: data}
	}
	writeRaw(w, status, data)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, _ := json.Marshal(v) // v — простые структуры, ошибки быть не может
	writeRaw(w, status, data)
}

func writeRaw(w http.ResponseWriter, status int, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
