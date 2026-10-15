package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

type webhookResponse struct {
	Result string `json:"result"`
}

// bankWebhook — POST /v1/webhooks/banks/{code}: асинхронное решение банка (раздел 5.3 спеки).
func (a *api) bankWebhook(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	wb, ok := a.WebhookBanks[code]
	if !ok {
		writeProblem(w, http.StatusNotFound, "Unknown bank", fmt.Sprintf("bank %q does not accept webhooks", code))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "Body too large", fmt.Sprintf("body exceeds %d bytes", maxBodyBytes))
			return
		}
		writeProblem(w, http.StatusBadRequest, "Invalid request body", "cannot read body")
		return
	}

	log := a.Log.With("bank_code", code)
	ts, sig := r.Header.Get(bank.HeaderWebhookTimestamp), r.Header.Get(bank.HeaderWebhookSignature)
	if err := bank.VerifyWebhook(wb.Secret, ts, sig, body, a.Now(), a.WebhookMaxSkew); err != nil {
		log.Warn("webhook rejected", "error", err)
		writeProblem(w, http.StatusUnauthorized, "Invalid signature", "webhook signature is missing, invalid or expired")
		return
	}
	externalID, decision, err := wb.Handler.ParseCallback(body)
	if err != nil {
		log.Warn("webhook body rejected", "error", err)
		writeProblem(w, http.StatusBadRequest, "Invalid callback", err.Error())
		return
	}

	log = log.With("external_id", externalID)
	res, err := a.Callbacks.ApplyCallback(r.Context(), code, externalID, decision)
	if err != nil {
		log.Error("apply webhook failed", "error", err)
		writeProblem(w, http.StatusInternalServerError, "Internal error", "")
		return
	}
	switch res.Result {
	case application.CallbackApplied:
		log.Info("webhook applied")
		writeJSON(w, http.StatusOK, webhookResponse{Result: "applied"})
	case application.CallbackDuplicate:
		log.Warn("webhook ignored: request is not waiting for a callback")
		writeJSON(w, http.StatusOK, webhookResponse{Result: "ignored"})
	default:
		log.Warn("webhook for unknown request")
		writeProblem(w, http.StatusNotFound, "Unknown request", "no request with this external id")
	}
}
