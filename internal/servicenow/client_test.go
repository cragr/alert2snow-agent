package servicenow

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/cragr/alert2snow-agent/internal/config"
	"github.com/cragr/alert2snow-agent/internal/models"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestClient_CreateIncident(t *testing.T) {
	var receivedBody models.ServiceNowIncident
	var receivedAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		user, pass, ok := r.BasicAuth()
		if !ok {
			t.Error("expected basic auth")
		}
		receivedAuth = user + ":" + pass

		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(models.ServiceNowResponse{
			Result: models.ServiceNowResult{
				SysID:  "abc123",
				Number: "INC0001234",
			},
		})
	}))
	defer server.Close()

	cfg := &config.Config{
		ServiceNowBaseURL:      server.URL,
		ServiceNowEndpointPath: "/api/now/table/incident",
		ServiceNowUsername:     "testuser",
		ServiceNowPassword:     "testpass",
	}

	client := NewClient(cfg, newTestLogger())
	// Disable retries for testing
	client.retryConfig.MaxAttempts = 1

	incident := models.ServiceNowIncident{
		ShortDescription: "[test-cluster] TestAlert in namespace: default",
		Description:      "Test description",
		Impact:           "3",
		Urgency:          "3",
		Category:         "software",
		Subcategory:      "openshift",
		CorrelationID:    "abc123def456",
	}

	result, err := client.CreateIncident(context.Background(), incident)
	if err != nil {
		t.Errorf("CreateIncident() error = %v", err)
	}

	if receivedAuth != "testuser:testpass" {
		t.Errorf("expected auth 'testuser:testpass', got %q", receivedAuth)
	}

	if receivedBody.CorrelationID != "abc123def456" {
		t.Errorf("expected correlation_id 'abc123def456', got %q", receivedBody.CorrelationID)
	}

	// Verify returned incident number
	if result == nil {
		t.Fatal("expected result, got nil")
	}
	if result.Number != "INC0001234" {
		t.Errorf("expected incident number 'INC0001234', got %q", result.Number)
	}
	if result.SysID != "abc123" {
		t.Errorf("expected sys_id 'abc123', got %q", result.SysID)
	}
}

func TestClient_FindIncidentByCorrelationID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}

		query := r.URL.Query().Get("sysparm_query")
		if query != "correlation_id=test-correlation-id" {
			t.Errorf("expected query 'correlation_id=test-correlation-id', got %q", query)
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(models.ServiceNowListResponse{
			Result: []models.ServiceNowResult{
				{
					SysID:         "sys123",
					Number:        "INC0001234",
					CorrelationID: "test-correlation-id",
				},
			},
		})
	}))
	defer server.Close()

	cfg := &config.Config{
		ServiceNowBaseURL:      server.URL,
		ServiceNowEndpointPath: "/api/now/table/incident",
		ServiceNowUsername:     "testuser",
		ServiceNowPassword:     "testpass",
	}

	client := NewClient(cfg, newTestLogger())
	client.retryConfig.MaxAttempts = 1

	result, err := client.FindIncidentByCorrelationID(context.Background(), "test-correlation-id")
	if err != nil {
		t.Errorf("FindIncidentByCorrelationID() error = %v", err)
	}

	if result == nil {
		t.Fatal("expected result, got nil")
	}

	if result.SysID != "sys123" {
		t.Errorf("expected sys_id 'sys123', got %q", result.SysID)
	}
}

func TestClient_FindIncidentByCorrelationID_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(models.ServiceNowListResponse{
			Result: []models.ServiceNowResult{},
		})
	}))
	defer server.Close()

	cfg := &config.Config{
		ServiceNowBaseURL:      server.URL,
		ServiceNowEndpointPath: "/api/now/table/incident",
		ServiceNowUsername:     "testuser",
		ServiceNowPassword:     "testpass",
	}

	client := NewClient(cfg, newTestLogger())
	client.retryConfig.MaxAttempts = 1

	result, err := client.FindIncidentByCorrelationID(context.Background(), "nonexistent")
	if err != nil {
		t.Errorf("FindIncidentByCorrelationID() error = %v", err)
	}

	if result != nil {
		t.Error("expected nil result for not found")
	}
}

func TestClient_ResolveIncident(t *testing.T) {
	var receivedBody models.ServiceNowUpdatePayload

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("expected PATCH, got %s", r.Method)
		}

		expectedPath := "/api/now/table/incident/sys123"
		if r.URL.Path != expectedPath {
			t.Errorf("expected path %q, got %q", expectedPath, r.URL.Path)
		}

		if err := json.NewDecoder(r.Body).Decode(&receivedBody); err != nil {
			t.Errorf("failed to decode request body: %v", err)
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(models.ServiceNowResponse{
			Result: models.ServiceNowResult{
				SysID: "sys123",
				State: "6",
			},
		})
	}))
	defer server.Close()

	cfg := &config.Config{
		ServiceNowBaseURL:      server.URL,
		ServiceNowEndpointPath: "/api/now/table/incident",
		ServiceNowUsername:     "testuser",
		ServiceNowPassword:     "testpass",
	}

	client := NewClient(cfg, newTestLogger())
	client.retryConfig.MaxAttempts = 1

	err := client.ResolveIncident(context.Background(), "sys123")
	if err != nil {
		t.Errorf("ResolveIncident() error = %v", err)
	}

	if receivedBody.State != "6" {
		t.Errorf("expected state '6', got %q", receivedBody.State)
	}
}

func TestClient_CreateIncident_ServerError(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error": "internal server error"}`))
	}))
	defer server.Close()

	cfg := &config.Config{
		ServiceNowBaseURL:      server.URL,
		ServiceNowEndpointPath: "/api/now/table/incident",
		ServiceNowUsername:     "testuser",
		ServiceNowPassword:     "testpass",
	}

	client := NewClient(cfg, newTestLogger())
	// Set max attempts to 2 for faster test
	client.retryConfig.MaxAttempts = 2
	client.retryConfig.BaseDelay = 1_000_000 // 1ms

	incident := models.ServiceNowIncident{
		ShortDescription: "Test",
		CorrelationID:    "test123",
	}

	_, err := client.CreateIncident(context.Background(), incident)
	if err == nil {
		t.Error("expected error for server error response")
	}

	if attempts != 2 {
		t.Errorf("expected 2 retry attempts, got %d", attempts)
	}
}

func TestClient_CreateIncident_ClientError_NoRetry(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error": "bad request"}`))
	}))
	defer server.Close()

	cfg := &config.Config{
		ServiceNowBaseURL:      server.URL,
		ServiceNowEndpointPath: "/api/now/table/incident",
		ServiceNowUsername:     "testuser",
		ServiceNowPassword:     "testpass",
	}

	client := NewClient(cfg, newTestLogger())
	client.retryConfig.MaxAttempts = 3

	incident := models.ServiceNowIncident{
		ShortDescription: "Test",
		CorrelationID:    "test123",
	}

	_, err := client.CreateIncident(context.Background(), incident)
	if err == nil {
		t.Error("expected error for client error response")
	}

	// Should not retry on 4xx errors
	if attempts != 1 {
		t.Errorf("expected 1 attempt (no retry on 4xx), got %d", attempts)
	}
}
