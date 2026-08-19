package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cragr/alert2snow-agent/internal/config"
	"github.com/cragr/alert2snow-agent/internal/metrics"
	"github.com/cragr/alert2snow-agent/internal/models"
	"github.com/cragr/alert2snow-agent/internal/servicenow"
)

// firingPayload builds an Alertmanager payload for a single firing alert.
func firingPayload(labels map[string]string) []byte {
	return alertPayload("firing", labels)
}

// resolvedPayload builds an Alertmanager payload for a single resolved alert.
func resolvedPayload(labels map[string]string) []byte {
	return alertPayload("resolved", labels)
}

func alertPayload(status string, labels map[string]string) []byte {
	p := models.AlertmanagerPayload{
		Version:  "4",
		Status:   status,
		Receiver: "servicenow-bridge",
		Alerts: []models.Alert{{
			Status:      status,
			Labels:      labels,
			Annotations: map[string]string{"summary": "test"},
			StartsAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
			Fingerprint: "abc123def456",
		}},
	}
	b, _ := json.Marshal(p)
	return b
}

func newReproHandler(mock *mockServiceNowClient) *Handler {
	cfg := &config.Config{
		ClusterLabelKey:     "cluster",
		EnvironmentLabelKey: "environment",
	}
	return NewHandler(mock, NewTransformer(cfg), metrics.NewNop(), newTestLogger())
}

// newStatefulFake returns a mock that behaves like ServiceNow: an incident
// created for a correlation ID is subsequently findable as open, and resolving
// it removes it from the open set — mirroring the state filter on the query.
// A stub that always reports "not found" cannot detect a missing dedup check.
func newStatefulFake() *mockServiceNowClient {
	m := &mockServiceNowClient{}
	open := make(map[string]models.ServiceNowResult)
	bySysID := make(map[string]string)

	m.createIncidentFn = func(_ context.Context, incident models.ServiceNowIncident) (*servicenow.CreateIncidentResult, error) {
		n := len(bySysID) + 1
		sysID := fmt.Sprintf("sys-%d", n)
		number := fmt.Sprintf("INC%07d", n)
		open[incident.CorrelationID] = models.ServiceNowResult{
			SysID:         sysID,
			Number:        number,
			State:         "1",
			CorrelationID: incident.CorrelationID,
		}
		bySysID[sysID] = incident.CorrelationID
		return &servicenow.CreateIncidentResult{SysID: sysID, Number: number}, nil
	}

	m.findIncidentByCorrelationFn = func(_ context.Context, correlationID string) (*models.ServiceNowResult, error) {
		if inc, ok := open[correlationID]; ok {
			return &inc, nil
		}
		return nil, nil
	}

	m.resolveIncidentFn = func(_ context.Context, sysID string) error {
		correlationID, ok := bySysID[sysID]
		if !ok {
			return fmt.Errorf("no such incident %s", sysID)
		}
		// Closed incidents are state 6/7/8, which the query excludes, so they
		// are no longer findable as open.
		delete(open, correlationID)
		return nil
	}

	return m
}

// TestRepeatNotificationDoesNotDuplicate reproduces the reported bug:
// Alertmanager re-notifies for a still-firing alert (group_interval /
// repeat_interval). The same alert arriving twice must NOT create two incidents.
func TestRepeatNotificationDoesNotDuplicate(t *testing.T) {
	mock := newStatefulFake()
	handler := newReproHandler(mock)

	labels := map[string]string{
		"alertname": "KubePodCrashLooping",
		"namespace": "prod",
		"severity":  "critical",
	}
	body := firingPayload(labels)

	// Alertmanager delivers the same firing alert twice.
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/alertmanager/webhook", bytes.NewReader(body))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("delivery %d: status = %d, want 200", i+1, rr.Code)
		}
	}

	if len(mock.createCalls) != 1 {
		t.Errorf("CreateIncident called %d times for the same firing alert, want 1 (duplicate incident created)",
			len(mock.createCalls))
	}
}

// TestCorrelationIDDependsOnFullLabelSet documents current, deliberate
// behaviour: the correlation ID is derived from every label, so two alerts that
// differ in any label are distinct incidents.
//
// This is what makes batch fan-out visible — Alertmanager groups by
// [alertname, namespace], so sibling alerts differing only in an ungrouped label
// arrive in one webhook and produce one incident each. Dedup does not merge them
// because by this definition they are different alerts.
//
// Narrowing the hash to a stable subset (alertname, cluster, namespace) would
// collapse siblings into a single incident, but it is a service-management
// decision about incident granularity, not a bug fix: it changes the correlation
// ID of every currently-open incident, which breaks auto-close for anything
// mid-flight. It must land on a boundary where nothing is firing. See
// docs/correlation-id.md.
func TestCorrelationIDDependsOnFullLabelSet(t *testing.T) {
	base := map[string]string{
		"alertname": "KubePodCrashLooping",
		"namespace": "prod",
		"severity":  "critical",
	}
	withExtra := map[string]string{
		"alertname":  "KubePodCrashLooping",
		"namespace":  "prod",
		"severity":   "critical",
		"prometheus": "openshift-monitoring/k8s",
	}

	if GenerateCorrelationID("KubePodCrashLooping", withExtra) == GenerateCorrelationID("KubePodCrashLooping", base) {
		t.Error("correlation ID ignored an added label; incident granularity changed without a decision")
	}
}

// TestCorrelationIDStableForIdenticalLabels is the property dedup actually
// relies on: the same alert re-notified by Alertmanager must hash identically,
// including when the map is built in a different order.
func TestCorrelationIDStableForIdenticalLabels(t *testing.T) {
	first := map[string]string{
		"alertname": "KubePodCrashLooping",
		"namespace": "prod",
		"severity":  "critical",
	}
	second := map[string]string{
		"severity":  "critical",
		"alertname": "KubePodCrashLooping",
		"namespace": "prod",
	}

	if got, want := GenerateCorrelationID("KubePodCrashLooping", second), GenerateCorrelationID("KubePodCrashLooping", first); got != want {
		t.Errorf("correlation ID not stable for identical labels:\n got = %s\nwant = %s", got, want)
	}
}

// TestCorrelationIDNoDelimiterCollision guards the hash canonicalisation
// against ambiguity: without delimiters, {"a":"bc","d":"e"} and
// {"a":"b","cd":"e"} both flatten to "abcde". Once dedup is on the firing path,
// a collision means one alert silently suppresses an unrelated one.
func TestCorrelationIDNoDelimiterCollision(t *testing.T) {
	a := map[string]string{"a": "bc", "d": "e"}
	b := map[string]string{"a": "b", "cd": "e"}

	if GenerateCorrelationID("X", a) == GenerateCorrelationID("X", b) {
		t.Errorf("distinct label sets %v and %v produced the same correlation ID (delimiter collision)", a, b)
	}
}

// TestRefireAfterCloseCreatesNewIncident is the case the state filter protects.
// Once the incident is closed, the same alert firing again must open a new one
// rather than being suppressed by the closed record.
func TestRefireAfterCloseCreatesNewIncident(t *testing.T) {
	mock := newStatefulFake()
	handler := newReproHandler(mock)

	labels := map[string]string{
		"alertname": "KubePodCrashLooping",
		"namespace": "prod",
		"severity":  "critical",
	}

	post := func(body []byte) {
		req := httptest.NewRequest(http.MethodPost, "/alertmanager/webhook", bytes.NewReader(body))
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", rr.Code)
		}
	}

	post(firingPayload(labels))   // fires, incident opened
	post(resolvedPayload(labels)) // clears, incident closed
	post(firingPayload(labels))   // fires again -> must be a new incident

	if len(mock.createCalls) != 2 {
		t.Errorf("CreateIncident called %d times, want 2 (re-fire after close was suppressed)",
			len(mock.createCalls))
	}
}

// TestResolvedAlertClosesEveryDuplicate covers the backlog of duplicates that
// already exist in production from before dedup. A resolved alert must close all
// of them, not just the first — the rest have no alert behind them and no future
// event that would ever close them.
func TestResolvedAlertClosesEveryDuplicate(t *testing.T) {
	mock := &mockServiceNowClient{
		findOpenIncidentsFn: func(_ context.Context, correlationID string) ([]models.ServiceNowResult, error) {
			return []models.ServiceNowResult{
				{SysID: "sys-1", Number: "INC0000001", CorrelationID: correlationID},
				{SysID: "sys-2", Number: "INC0000002", CorrelationID: correlationID},
				{SysID: "sys-3", Number: "INC0000003", CorrelationID: correlationID},
			}, nil
		},
	}
	handler := newReproHandler(mock)

	body := resolvedPayload(map[string]string{
		"alertname": "KubePodCrashLooping",
		"namespace": "prod",
		"severity":  "critical",
	})
	req := httptest.NewRequest(http.MethodPost, "/alertmanager/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if len(mock.resolveCalls) != 3 {
		t.Fatalf("ResolveIncident called %d times, want 3 (duplicates left open)", len(mock.resolveCalls))
	}
	for i, want := range []string{"sys-1", "sys-2", "sys-3"} {
		if mock.resolveCalls[i] != want {
			t.Errorf("resolve call %d = %q, want %q", i, mock.resolveCalls[i], want)
		}
	}
}

// TestResolveContinuesAfterOneFailure asserts a single unresolvable incident
// does not abort the loop and leave the remaining duplicates open.
func TestResolveContinuesAfterOneFailure(t *testing.T) {
	mock := &mockServiceNowClient{
		findOpenIncidentsFn: func(_ context.Context, correlationID string) ([]models.ServiceNowResult, error) {
			return []models.ServiceNowResult{
				{SysID: "sys-1", Number: "INC0000001"},
				{SysID: "sys-2", Number: "INC0000002"},
				{SysID: "sys-3", Number: "INC0000003"},
			}, nil
		},
		resolveIncidentFn: func(_ context.Context, sysID string) error {
			if sysID == "sys-2" {
				return errors.New("ServiceNow rejected the update")
			}
			return nil
		},
	}
	handler := newReproHandler(mock)

	body := resolvedPayload(map[string]string{"alertname": "KubePodCrashLooping"})
	req := httptest.NewRequest(http.MethodPost, "/alertmanager/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if len(mock.resolveCalls) != 3 {
		t.Errorf("ResolveIncident called %d times, want 3 (loop aborted on first failure)",
			len(mock.resolveCalls))
	}
}

// TestTotalFailureReturns500 covers the silent-loss case: when every alert in a
// payload fails, Alertmanager must see a non-2xx so it retries, rather than
// treating a ServiceNow outage as delivered.
func TestTotalFailureReturns500(t *testing.T) {
	mock := &mockServiceNowClient{
		createIncidentFn: func(_ context.Context, _ models.ServiceNowIncident) (*servicenow.CreateIncidentResult, error) {
			return nil, errors.New("ServiceNow unreachable")
		},
	}
	handler := newReproHandler(mock)

	body := firingPayload(map[string]string{"alertname": "KubePodCrashLooping"})
	req := httptest.NewRequest(http.MethodPost, "/alertmanager/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (total delivery failure reported as success)", rr.Code)
	}
}

// TestPartialFailureReturns200 is the counterpart: retrying a batch that partly
// succeeded would re-deliver the alerts that worked, so partial failure stays
// 200 and is surfaced through logs and metrics instead.
func TestPartialFailureReturns200(t *testing.T) {
	var calls int
	mock := &mockServiceNowClient{
		createIncidentFn: func(_ context.Context, _ models.ServiceNowIncident) (*servicenow.CreateIncidentResult, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("ServiceNow rejected this one")
			}
			return &servicenow.CreateIncidentResult{SysID: "sys-ok", Number: "INC0000002"}, nil
		},
	}
	handler := newReproHandler(mock)

	payload := models.AlertmanagerPayload{
		Version: "4",
		Status:  "firing",
		Alerts: []models.Alert{
			{Status: "firing", Labels: map[string]string{"alertname": "Alert1"}},
			{Status: "firing", Labels: map[string]string{"alertname": "Alert2"}},
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/alertmanager/webhook", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (partial failure must not trigger a batch retry)", rr.Code)
	}
}
