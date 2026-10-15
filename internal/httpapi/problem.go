package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

// maxBodyBytes — предел размера тела запроса.
const maxBodyBytes = 1 << 20

// problem — ошибка в формате RFC 9457 (application/problem+json).
type problem struct {
	Type   string                   `json:"type"`
	Title  string                   `json:"title"`
	Status int                      `json:"status"`
	Detail string                   `json:"detail,omitempty"`
	Errors []application.FieldError `json:"errors,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeProblem(w http.ResponseWriter, status int, title, detail string, fields ...application.FieldError) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{Type: "about:blank", Title: title, Status: status, Detail: detail, Errors: fields})
}
