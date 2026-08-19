package webhook

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/cragr/alert2snow-agent/internal/config"
	"github.com/cragr/alert2snow-agent/internal/metrics"
	"github.com/cragr/alert2snow-agent/internal/models"
	"github.com/cragr/alert2snow-agent/internal/servicenow"
)

// newInstrumentedHandler returns a handler wired to a private registry so the
// test can assert on real counter values rather than trusting that the
// instrumentation calls exist.
func newInstrumentedHandler(mock *mockServiceNowClient) (*Handler, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	cfg := &config.Config{ClusterLabelKey: "cluster", EnvironmentLabelKey: "environment"}
	return NewHandler(mock, NewTransformer(cfg), metrics.New(reg), newTestLogger()), reg
}

func post(t *testing.T, h *Handler, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/alertmanager/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// counterTotal sums every series of a counter family. A CounterVec that has not
// observed any label combination emits no family at all, which is indistinguishable
// from zero to a scraper, so absent is reported as 0 rather than as an error.
func counterTotal(t *testing.T, reg *prometheus.Registry, name string) float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	total := 0.0
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, met := range f.GetMetric() {
			total += met.GetCounter().GetValue()
		}
	}
	return total
}

func wantCounter(t *testing.T, reg *prometheus.Registry, name string, want float64) {
	t.Helper()
	if got := counterTotal(t, reg, name); got != want {
		t.Errorf("%s = %v, want %v", name, got, want)
	}
}

// TestMetrics_SkippedCounterMovesOnDuplicate covers the metric that proves dedup
// is working in production. If this counter stays flat while duplicates stop, the
// instrumentation is lying.
func TestMetrics_SkippedCounterMovesOnDuplicate(t *testing.T) {
	mock := newStatefulFake()
	handler, reg := newInstrumentedHandler(mock)

	body := firingPayload(map[string]string{
		"alertname": "KubePodCrashLooping",
		"namespace": "prod",
		"severity":  "critical",
	})

	post(t, handler, body) // creates
	post(t, handler, body) // skips
	post(t, handler, body) // skips

	wantCounter(t, reg, "alert2snow_incidents_created_total", 1)
	wantCounter(t, reg, "alert2snow_incidents_skipped_total", 2)
	wantCounter(t, reg, "alert2snow_alerts_received_total", 3)
	wantCounter(t, reg, "alert2snow_alerts_failed_total", 0)
}

// TestMetrics_FailureCounterMovesOnError covers the case that was previously
// invisible: ServiceNow rejecting everything.
func TestMetrics_FailureCounterMovesOnError(t *testing.T) {
	mock := &mockServiceNowClient{
		createIncidentFn: func(_ context.Context, _ models.ServiceNowIncident) (*servicenow.CreateIncidentResult, error) {
			return nil, errors.New("ServiceNow unreachable")
		},
	}
	handler, reg := newInstrumentedHandler(mock)

	rr := post(t, handler, firingPayload(map[string]string{"alertname": "KubePodCrashLooping"}))

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rr.Code)
	}
	wantCounter(t, reg, "alert2snow_alerts_failed_total", 1)
	wantCounter(t, reg, "alert2snow_incidents_created_total", 0)
}

// TestMetrics_ResolvedCounterCountsEveryIncident asserts the resolved counter
// tracks incidents, not alerts: one resolved alert closing three pre-dedup
// duplicates must count three.
func TestMetrics_ResolvedCounterCountsEveryIncident(t *testing.T) {
	mock := &mockServiceNowClient{
		findOpenIncidentsFn: func(_ context.Context, _ string) ([]models.ServiceNowResult, error) {
			return []models.ServiceNowResult{
				{SysID: "sys-1", Number: "INC0000001"},
				{SysID: "sys-2", Number: "INC0000002"},
				{SysID: "sys-3", Number: "INC0000003"},
			}, nil
		},
	}
	handler, reg := newInstrumentedHandler(mock)

	post(t, handler, resolvedPayload(map[string]string{"alertname": "KubePodCrashLooping"}))

	wantCounter(t, reg, "alert2snow_incidents_resolved_total", 3)
	wantCounter(t, reg, "alert2snow_alerts_received_total", 1)
}
