package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/cragr/alert2snow-agent/internal/metrics"
	"github.com/cragr/alert2snow-agent/internal/models"
	"github.com/cragr/alert2snow-agent/internal/servicenow"
)

// ServiceNowClient defines the interface for ServiceNow operations.
type ServiceNowClient interface {
	CreateIncident(ctx context.Context, incident models.ServiceNowIncident) (*servicenow.CreateIncidentResult, error)
	FindIncidentByCorrelationID(ctx context.Context, correlationID string) (*models.ServiceNowResult, error)
	FindOpenIncidentsByCorrelationID(ctx context.Context, correlationID string) ([]models.ServiceNowResult, error)
	ResolveIncident(ctx context.Context, sysID string) error
}

// Handler handles Alertmanager webhook requests.
type Handler struct {
	snowClient  ServiceNowClient
	transformer *Transformer
	metrics     *metrics.Metrics
	logger      *slog.Logger
}

// NewHandler creates a new webhook handler.
func NewHandler(snowClient ServiceNowClient, transformer *Transformer, m *metrics.Metrics, logger *slog.Logger) *Handler {
	return &Handler{
		snowClient:  snowClient,
		transformer: transformer,
		metrics:     m,
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
		h.metrics.AlertReceived(alert.Status)

		if err := h.processAlert(ctx, alert, payload.ExternalURL); err != nil {
			h.logger.Error("failed to process alert",
				"alertname", alert.Labels["alertname"],
				"status", alert.Status,
				"error", err,
			)
			h.metrics.AlertFailed(alert.Status)
			errCount++
		}
	}

	if errCount > 0 {
		h.logger.Warn("some alerts failed to process",
			"total", len(payload.Alerts),
			"failed", errCount,
		)
	}

	// When every alert in the payload failed, the cause is almost certainly
	// systemic — ServiceNow unreachable, credentials rejected — rather than one
	// bad alert. Return 500 so Alertmanager retries and the failure is visible
	// instead of silently dropping critical incidents.
	//
	// Partial failures still return 200: retrying the whole batch would
	// re-deliver the alerts that succeeded. This is safe to pair with the dedup
	// check on the firing path, which finds the already-open incidents and skips
	// them on the retry.
	if errCount > 0 && errCount == len(payload.Alerts) {
		http.Error(w, `{"status":"error"}`, http.StatusInternalServerError)
		return
	}

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

// handleFiringAlert creates an incident in ServiceNow, unless one is already
// open for this correlation ID.
func (h *Handler) handleFiringAlert(ctx context.Context, alert models.Alert, externalURL, correlationID string) error {
	alertname := alert.Labels["alertname"]

	h.logger.Info("processing firing alert",
		"alertname", alertname,
		"correlation_id", correlationID,
	)

	// Alertmanager re-notifies for still-firing alerts every repeat_interval,
	// and a failed-then-retried delivery arrives twice. Without this check each
	// re-notification opens another incident for the same condition.
	existing, err := h.snowClient.FindIncidentByCorrelationID(ctx, correlationID)
	if err != nil {
		return fmt.Errorf("failed to check for existing incident: %w", err)
	}
	if existing != nil {
		h.logger.Info("open incident already exists, skipping creation",
			"alertname", alertname,
			"correlation_id", correlationID,
			"incident_number", existing.Number,
			"sys_id", existing.SysID,
		)
		h.metrics.IncidentSkipped(alertname)
		return nil
	}

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
	h.metrics.IncidentCreated(alertname)

	return nil
}

// handleResolvedAlert resolves every open incident matching the correlation ID.
// More than one can exist for alerts that fired before dedup was in place.
func (h *Handler) handleResolvedAlert(ctx context.Context, correlationID, alertname string) error {
	h.logger.Info("processing resolved alert",
		"alertname", alertname,
		"correlation_id", correlationID,
	)

	existing, err := h.snowClient.FindOpenIncidentsByCorrelationID(ctx, correlationID)
	if err != nil {
		return err
	}

	if len(existing) == 0 {
		h.logger.Warn("no open incident found for resolved alert",
			"alertname", alertname,
			"correlation_id", correlationID,
		)
		return nil
	}

	// Collect failures rather than returning on the first one: a single
	// unresolvable incident must not leave the remaining duplicates open with
	// no alert behind them and no future event that would ever close them.
	var errs []error
	for _, inc := range existing {
		if err := h.snowClient.ResolveIncident(ctx, inc.SysID); err != nil {
			h.logger.Error("failed to resolve incident",
				"alertname", alertname,
				"correlation_id", correlationID,
				"incident_number", inc.Number,
				"sys_id", inc.SysID,
				"error", err,
			)
			errs = append(errs, fmt.Errorf("resolve %s: %w", inc.Number, err))
			continue
		}

		h.logger.Info("resolved incident in ServiceNow",
			"alertname", alertname,
			"correlation_id", correlationID,
			"sys_id", inc.SysID,
			"incident_number", inc.Number,
		)
		h.metrics.IncidentResolved()
	}

	return errors.Join(errs...)
}
