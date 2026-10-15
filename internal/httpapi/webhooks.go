package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/telemetry"
)

type webhookResponse struct {
	Result string `json:"result"`
}

// bankWebhook — POST /v1/webhooks/banks/{code}: асинхронное решение банка (раздел 5.3 спеки).
func (a *api) bankWebhook(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	wb, ok := a.WebhookBanks[code]
	if !ok {
		// Код из пути в метку не идёт: произвольные коды плодили бы серии.
		a.Metrics.Webhook(telemetry.Other, telemetry.WebhookUnknownBank)
		writeProblem(w, http.StatusNotFound, "Unknown bank", fmt.Sprintf("bank %q does not accept webhooks", code))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		a.Metrics.Webhook(code, telemetry.WebhookBadBody)
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "Body too large", fmt.Sprintf("body exceeds %d bytes", maxBodyBytes))
			return
		}
		writeProblem(w, http.StatusBadRequest, "Invalid request body", "cannot read body")
		return
	}
	ctx := r.Context()
	log := a.Log.With("bank_code", code)
	ts, sig := r.Header.Get(bank.HeaderWebhookTimestamp), r.Header.Get(bank.HeaderWebhookSignature)
	if err := bank.VerifyWebhook(wb.Secret, ts, sig, body, a.Now(), a.WebhookMaxSkew); err != nil {
		a.Metrics.Webhook(code, telemetry.WebhookUnauthorized)
		log.WarnContext(ctx, "webhook rejected", "error", err)
		writeProblem(w, http.StatusUnauthorized, "Invalid signature", "webhook signature is missing, invalid or expired")
		return
	}
	externalID, decision, err := wb.Handler.ParseCallback(body)
	if err != nil {
		a.Metrics.Webhook(code, telemetry.WebhookBadBody)
		log.WarnContext(ctx, "webhook body rejected", "error", err)
		writeProblem(w, http.StatusBadRequest, "Invalid callback", err.Error())
		return
	}
	log = log.With("external_id", externalID)
	start := time.Now()
	res, err := a.Callbacks.ApplyCallback(ctx, code, externalID, decision)
	if err != nil {
		a.Metrics.Webhook(code, telemetry.WebhookError)
		log.ErrorContext(ctx, "apply webhook failed", "error", err)
		writeProblem(w, http.StatusInternalServerError, "Internal error", "")
		return
	}
	switch res.Result {
	case application.CallbackApplied:
		a.Metrics.Webhook(code, telemetry.WebhookApplied)
		a.Metrics.Decision(code, decision.Approved, telemetry.ViaWebhook)
		a.traceCallback(ctx, code, res, decision, start)
		log.InfoContext(ctx, "webhook applied")
		writeJSON(w, http.StatusOK, webhookResponse{Result: "applied"})
	case application.CallbackDuplicate:
		a.Metrics.Webhook(code, telemetry.WebhookDuplicate)
		log.WarnContext(ctx, "webhook ignored: request is not waiting for a callback")
		writeJSON(w, http.StatusOK, webhookResponse{Result: "ignored"})
	default:
		a.Metrics.Webhook(code, telemetry.WebhookUnknown)
		log.WarnContext(ctx, "webhook for unknown request")
		writeProblem(w, http.StatusNotFound, "Unknown request", "no request with this external id")
	}
}

// traceCallback добавляет в трейс заявки спан применённого вебхука (раздел 14.4 спеки).
// Банк наш контекст не присылает, поэтому родитель — trace_parent запроса, а со спаном самого
// вебхука новый спан связан ссылкой.
func (a *api) traceCallback(ctx context.Context, code string, res application.CallbackApply, d application.Decision, start time.Time) {
	if res.TraceParent == "" {
		return
	}
	status := application.RequestRejected
	if d.Approved {
		status = application.RequestApproved
	}
	_, span := a.Tracer.Start(telemetry.WithTraceParent(ctx, res.TraceParent), "bank_request.callback",
		trace.WithTimestamp(start),
		trace.WithLinks(trace.Link{SpanContext: trace.SpanContextFromContext(ctx)}),
		trace.WithAttributes(
			attribute.String("bank_request.id", res.RequestID.String()),
			attribute.String("bank.code", code),
			attribute.String("decision.status", string(status)),
		))
	span.End()
}
