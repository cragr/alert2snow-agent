package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cragr/alert2snow-agent/internal/config"
	"github.com/cragr/alert2snow-agent/internal/models"
	"github.com/cragr/alert2snow-agent/internal/servicenow"
)

// mockServiceNowClient implements ServiceNowClient for testing.
type mockServiceNowClient struct {
	createIncidentFn            func(ctx context.Context, incident models.ServiceNowIncident) (*servicenow.CreateIncidentResult, error)
	findIncidentByCorrelationFn func(ctx context.Context, correlationID string) (*models.ServiceNowResult, error)
	resolveIncidentFn           func(ctx context.Context, sysID string) error

	createCalls  []models.ServiceNowIncident
	resolveCalls []string
}

func (m *mockServiceNowClient) CreateIncident(ctx context.Context, incident models.ServiceNowIncident) (*servicenow.CreateIncidentResult, error) {
	m.createCalls = append(m.createCalls, incident)
	if m.createIncidentFn != nil {
		return m.createIncidentFn(ctx, incident)
	}
	// Return a default result for tests
	return &servicenow.CreateIncidentResult{
		SysID:  "mock-sys-id",
		Number: "INC0000001",
	}, nil
}

func (m *mockServiceNowClient) FindIncidentByCorrelationID(ctx context.Context, correlationID string) (*models.ServiceNowResult, error) {
	if m.findIncidentByCorrelationFn != nil {
		return m.findIncidentByCorrelationFn(ctx, correlationID)
	}
	return nil, nil
}

func (m *mockServiceNowClient) ResolveIncident(ctx context.Context, sysID string) error {
	m.resolveCalls = append(m.resolveCalls, sysID)
	if m.resolveIncidentFn != nil {
		return m.resolveIncidentFn(ctx, sysID)
	}
	return nil
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestHandler_ServeHTTP_FiringAlert(t *testing.T) {
	mockClient := &mockServiceNowClient{}
	cfg := &config.Config{
		ClusterLabelKey:       "cluster",
		EnvironmentLabelKey:   "environment",
		ServiceNowCategory:    "software",
		ServiceNowSubcategory: "openshift",
	}
	transformer := NewTransformer(cfg)
	handler := NewHandler(mockClient, transformer, newTestLogger())

	payload := models.AlertmanagerPayload{
		Version:  "4",
		Status:   "firing",
		Receiver: "test-receiver",
		Alerts: []models.Alert{
			{
				Status: "firing",
				Labels: map[string]string{
					"alertname": "TestAlert",
					"cluster":   "test-cluster",
					"severity":  "warning",
				},
				Annotations: map[string]string{
					"summary": "Test summary",
				},
				StartsAt: time.Now(),
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/alertmanager/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
	}

	if len(mockClient.createCalls) != 1 {
		t.Errorf("expected 1 CreateIncident call, got %d", len(mockClient.createCalls))
	}

	if mockClient.createCalls[0].ShortDescription == "" {
		t.Error("expected ShortDescription to be set")
	}
}

func TestHandler_ServeHTTP_ResolvedAlert(t *testing.T) {
	mockClient := &mockServiceNowClient{
		findIncidentByCorrelationFn: func(ctx context.Context, correlationID string) (*models.ServiceNowResult, error) {
			return &models.ServiceNowResult{
				SysID:  "abc123",
				Number: "INC0001234",
			}, nil
		},
	}
	cfg := &config.Config{
		ClusterLabelKey:       "cluster",
		EnvironmentLabelKey:   "environment",
		ServiceNowCategory:    "software",
		ServiceNowSubcategory: "openshift",
	}
	transformer := NewTransformer(cfg)
	handler := NewHandler(mockClient, transformer, newTestLogger())

	payload := models.AlertmanagerPayload{
		Version:  "4",
		Status:   "resolved",
		Receiver: "test-receiver",
		Alerts: []models.Alert{
			{
				Status: "resolved",
				Labels: map[string]string{
					"alertname": "TestAlert",
					"cluster":   "test-cluster",
					"severity":  "warning",
				},
				StartsAt: time.Now().Add(-1 * time.Hour),
				EndsAt:   time.Now(),
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/alertmanager/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
	}

	if len(mockClient.resolveCalls) != 1 {
		t.Errorf("expected 1 ResolveIncident call, got %d", len(mockClient.resolveCalls))
	}

	if mockClient.resolveCalls[0] != "abc123" {
		t.Errorf("expected resolve call with sys_id 'abc123', got %q", mockClient.resolveCalls[0])
	}
}

func TestHandler_ServeHTTP_ResolvedAlert_NoExistingIncident(t *testing.T) {
	mockClient := &mockServiceNowClient{
		findIncidentByCorrelationFn: func(ctx context.Context, correlationID string) (*models.ServiceNowResult, error) {
			return nil, nil // No existing incident
		},
	}
	cfg := &config.Config{
		ClusterLabelKey:       "cluster",
		EnvironmentLabelKey:   "environment",
		ServiceNowCategory:    "software",
		ServiceNowSubcategory: "openshift",
	}
	transformer := NewTransformer(cfg)
	handler := NewHandler(mockClient, transformer, newTestLogger())

	payload := models.AlertmanagerPayload{
		Version: "4",
		Status:  "resolved",
		Alerts: []models.Alert{
			{
				Status: "resolved",
				Labels: map[string]string{
					"alertname": "TestAlert",
				},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/alertmanager/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
	}

	// Should not call resolve if no existing incident
	if len(mockClient.resolveCalls) != 0 {
		t.Errorf("expected 0 ResolveIncident calls, got %d", len(mockClient.resolveCalls))
	}
}

func TestHandler_ServeHTTP_InvalidJSON(t *testing.T) {
	mockClient := &mockServiceNowClient{}
	cfg := &config.Config{
		ClusterLabelKey:       "cluster",
		EnvironmentLabelKey:   "environment",
		ServiceNowCategory:    "software",
		ServiceNowSubcategory: "openshift",
	}
	transformer := NewTransformer(cfg)
	handler := NewHandler(mockClient, transformer, newTestLogger())

	req := httptest.NewRequest(http.MethodPost, "/alertmanager/webhook", bytes.NewReader([]byte("invalid json")))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusBadRequest)
	}
}

func TestHandler_ServeHTTP_MethodNotAllowed(t *testing.T) {
	mockClient := &mockServiceNowClient{}
	cfg := &config.Config{
		ClusterLabelKey:       "cluster",
		EnvironmentLabelKey:   "environment",
		ServiceNowCategory:    "software",
		ServiceNowSubcategory: "openshift",
	}
	transformer := NewTransformer(cfg)
	handler := NewHandler(mockClient, transformer, newTestLogger())

	req := httptest.NewRequest(http.MethodGet, "/alertmanager/webhook", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandler_ServeHTTP_MultipleAlerts(t *testing.T) {
	mockClient := &mockServiceNowClient{}
	cfg := &config.Config{
		ClusterLabelKey:       "cluster",
		EnvironmentLabelKey:   "environment",
		ServiceNowCategory:    "software",
		ServiceNowSubcategory: "openshift",
	}
	transformer := NewTransformer(cfg)
	handler := NewHandler(mockClient, transformer, newTestLogger())

	payload := models.AlertmanagerPayload{
		Version: "4",
		Status:  "firing",
		Alerts: []models.Alert{
			{
				Status: "firing",
				Labels: map[string]string{"alertname": "Alert1"},
			},
			{
				Status: "firing",
				Labels: map[string]string{"alertname": "Alert2"},
			},
			{
				Status: "firing",
				Labels: map[string]string{"alertname": "Alert3"},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/alertmanager/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
	}

	if len(mockClient.createCalls) != 3 {
		t.Errorf("expected 3 CreateIncident calls, got %d", len(mockClient.createCalls))
	}
}

// TestHandler_ServeHTTP_ResolvedPayloadFile tests using the test-payload-resolved.json file
func TestHandler_ServeHTTP_ResolvedPayloadFile(t *testing.T) {
	// Find the project root by looking for go.mod
	projectRoot := findProjectRoot(t)
	payloadPath := filepath.Join(projectRoot, "test-payload-resolved.json")

	body, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Skipf("skipping test: could not read test-payload-resolved.json: %v", err)
	}

	// Parse the payload to verify it's valid
	var payload models.AlertmanagerPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("failed to parse test-payload-resolved.json: %v", err)
	}

	// Verify it's a resolved alert
	if payload.Status != "resolved" {
		t.Errorf("expected payload status 'resolved', got %q", payload.Status)
	}
	if len(payload.Alerts) == 0 {
		t.Fatal("expected at least one alert in payload")
	}
	if payload.Alerts[0].Status != "resolved" {
		t.Errorf("expected alert status 'resolved', got %q", payload.Alerts[0].Status)
	}

	// Test that handler processes the resolved alert correctly
	mockClient := &mockServiceNowClient{
		findIncidentByCorrelationFn: func(ctx context.Context, correlationID string) (*models.ServiceNowResult, error) {
			return &models.ServiceNowResult{
				SysID:  "existing-sys-id",
				Number: "INC0009999",
			}, nil
		},
	}
	cfg := &config.Config{
		ClusterLabelKey:       "cluster",
		EnvironmentLabelKey:   "environment",
		ServiceNowCategory:    "software",
		ServiceNowSubcategory: "openshift",
	}
	transformer := NewTransformer(cfg)
	handler := NewHandler(mockClient, transformer, newTestLogger())

	req := httptest.NewRequest(http.MethodPost, "/alertmanager/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("handler returned wrong status code: got %v want %v", rr.Code, http.StatusOK)
	}

	// Should not create new incidents for resolved alerts
	if len(mockClient.createCalls) != 0 {
		t.Errorf("expected 0 CreateIncident calls for resolved alert, got %d", len(mockClient.createCalls))
	}

	// Should call resolve for the existing incident
	if len(mockClient.resolveCalls) != 1 {
		t.Errorf("expected 1 ResolveIncident call, got %d", len(mockClient.resolveCalls))
	}
	if len(mockClient.resolveCalls) > 0 && mockClient.resolveCalls[0] != "existing-sys-id" {
		t.Errorf("expected resolve call with sys_id 'existing-sys-id', got %q", mockClient.resolveCalls[0])
	}
}

// findProjectRoot walks up the directory tree to find the project root (containing go.mod)
func findProjectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (go.mod)")
		}
		dir = parent
	}
}
