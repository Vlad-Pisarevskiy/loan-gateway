package httpapi

import (
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
)

// untracedRoutes — служебные маршруты: учитываются в метриках, но спанов не создают (раздел 14.4 спеки).
var untracedRoutes = map[string]bool{"/healthz": true, "/readyz": true}

// knownMethods — значения метки method; прочие методы считаются как other, иначе сканер плодил бы серии.
var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
}

// instrument оборачивает обработчик маршрута route серверным спаном и метриками HTTP (раздел 14 спеки).
// route — шаблон из таблицы маршрутов или telemetry.Other, а не сырой путь.
func (a *api) instrument(route string, next http.HandlerFunc) http.HandlerFunc {
	traced := !untracedRoutes[route]
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		method := r.Method
		if !knownMethods[method] {
			method = telemetry.Other
		}
		rec := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		if traced {
			ctx := propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
			ctx, span := a.Tracer.Start(ctx, method+" "+route,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(attribute.String("http.request.method", method), attribute.String("http.route", route)))
			defer func() {
				span.SetAttributes(attribute.Int("http.response.status_code", rec.code))
				if rec.code >= http.StatusInternalServerError {
					span.SetStatus(codes.Error, http.StatusText(rec.code))
				}
				span.End()
			}()
			r = r.WithContext(ctx)
		}
		next(rec, r)
		a.Metrics.HTTPRequest(route, method, rec.code, time.Since(start))
	}
}

// statusRecorder запоминает код ответа для метрик и спана.
type statusRecorder struct {
	http.ResponseWriter
	code        int
	wroteHeader bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wroteHeader {
		s.code, s.wroteHeader = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	s.wroteHeader = true
	return s.ResponseWriter.Write(b)
}

// Unwrap даёт http.ResponseController доступ к исходному ResponseWriter.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
