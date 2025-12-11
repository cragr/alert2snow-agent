package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/cragr/alert2snow-agent/internal/models"
	"github.com/cragr/alert2snow-agent/internal/servicenow"
)

// ServiceNowClient defines the interface for ServiceNow operations.
type ServiceNowClient interface {
	CreateIncident(ctx context.Context, incident models.ServiceNowIncident) (*servicenow.CreateIncidentResult, error)
	FindIncidentByCorrelationID(ctx context.Context, correlationID string) (*models.ServiceNowResult, error)
	ResolveIncident(ctx context.Context, sysID string) error
}

// Handler handles Alertmanager webhook requests.
type Handler struct {
	snowClient  ServiceNowClient
	transformer *Transformer
	logger      *slog.Logger
}

// NewHandler creates a new webhook handler.
func NewHandler(snowClient ServiceNowClient, transformer *Transformer, logger *slog.Logger) *Handler {
	return &Handler{
		snowClient:  snowClient,
		transformer: transformer,
		logger:      logger,
	}
}

// ServeHTTP handles incoming webhook requests from Alertmanager.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logger.Error("failed to read request body", "error", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var payload models.AlertmanagerPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		h.logger.Error("failed to parse alertmanager payload", "error", err)
		http.Error(w, "Invalid JSON payload", http.StatusBadRequest)
		return
	}

	h.logger.Info("received alertmanager webhook",
		"alert_count", len(payload.Alerts),
		"status", payload.Status,
		"receiver", payload.Receiver,
	)

	ctx := r.Context()
	var errCount int

	for _, alert := range payload.Alerts {
		if err := h.processAlert(ctx, alert, payload.ExternalURL); err != nil {
			h.logger.Error("failed to process alert",
				"alertname", alert.Labels["alertname"],
				"status", alert.Status,
				"error", err,
			)
			errCount++
		}
	}

	if errCount > 0 {
		h.logger.Warn("some alerts failed to process",
			"total", len(payload.Alerts),
			"failed", errCount,
		)
	}

	// Return 200 OK even if some alerts failed to prevent Alertmanager from retrying
	// the entire batch. Individual failures are logged for investigation.
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// processAlert handles a single alert based on its status.
func (h *Handler) processAlert(ctx context.Context, alert models.Alert, externalURL string) error {
	alertname := alert.Labels["alertname"]
	correlationID := GenerateCorrelationID(alertname, alert.Labels)

	switch alert.Status {
	case models.AlertStatusFiring:
		return h.handleFiringAlert(ctx, alert, externalURL, correlationID)
	case models.AlertStatusResolved:
		return h.handleResolvedAlert(ctx, correlationID, alertname)
	default:
		h.logger.Warn("unknown alert status",
			"alertname", alertname,
			"status", alert.Status,
		)
		return nil
	}
}

// handleFiringAlert creates a new incident in ServiceNow.
func (h *Handler) handleFiringAlert(ctx context.Context, alert models.Alert, externalURL, correlationID string) error {
	alertname := alert.Labels["alertname"]

	h.logger.Info("processing firing alert",
		"alertname", alertname,
		"correlation_id", correlationID,
	)

	incident := h.transformer.Transform(alert, externalURL)

	result, err := h.snowClient.CreateIncident(ctx, incident)
	if err != nil {
		return err
	}

	h.logger.Info("created incident in ServiceNow",
		"alertname", alertname,
		"correlation_id", correlationID,
		"incident_number", result.Number,
		"sys_id", result.SysID,
	)

	return nil
}

// handleResolvedAlert resolves an existing incident in ServiceNow.
func (h *Handler) handleResolvedAlert(ctx context.Context, correlationID, alertname string) error {
	h.logger.Info("processing resolved alert",
		"alertname", alertname,
		"correlation_id", correlationID,
	)

	// Find existing incident by correlation ID
	existing, err := h.snowClient.FindIncidentByCorrelationID(ctx, correlationID)
	if err != nil {
		return err
	}

	if existing == nil {
		h.logger.Warn("no existing incident found for resolved alert",
			"alertname", alertname,
			"correlation_id", correlationID,
		)
		return nil
	}

	// Resolve the incident
	if err := h.snowClient.ResolveIncident(ctx, existing.SysID); err != nil {
		return err
	}

	h.logger.Info("resolved incident in ServiceNow",
		"alertname", alertname,
		"correlation_id", correlationID,
		"sys_id", existing.SysID,
		"incident_number", existing.Number,
	)

	return nil
}
