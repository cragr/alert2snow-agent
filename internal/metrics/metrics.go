// Package metrics defines the agent's Prometheus instrumentation.
//
// The collectors live here rather than in package main because package main
// cannot be imported by any other package, so nothing under internal/ could
// reach them. A Metrics value is constructed once at startup and injected into
// the webhook handler and the ServiceNow client.
package metrics

import "github.com/prometheus/client_golang/prometheus"

// Operation labels for ServiceNow API calls.
const (
	OpCreate  = "create"
	OpFind    = "find"
	OpResolve = "resolve"
)

// Outcome labels for ServiceNow API calls.
const (
	OutcomeSuccess = "success"
	OutcomeError   = "error"
)

// Metrics holds the agent's Prometheus collectors.
type Metrics struct {
	alertsReceived     *prometheus.CounterVec
	incidentsCreated   *prometheus.CounterVec
	incidentsResolved  prometheus.Counter
	incidentsSkipped   *prometheus.CounterVec
	alertsFailed       *prometheus.CounterVec
	serviceNowRequests *prometheus.CounterVec
	serviceNowDuration *prometheus.HistogramVec
}

// New creates the agent's collectors and registers them with reg.
// Passing prometheus.DefaultRegisterer wires them to the default /metrics
// handler; tests can pass a fresh prometheus.NewRegistry().
func New(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		alertsReceived: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "alert2snow_alerts_received_total",
				Help: "Total alerts received from Alertmanager, by alert status.",
			},
			[]string{"status"},
		),
		incidentsCreated: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "alert2snow_incidents_created_total",
				Help: "Total incidents created in ServiceNow.",
			},
			[]string{"alertname"},
		),
		incidentsResolved: prometheus.NewCounter(
			prometheus.CounterOpts{
				Name: "alert2snow_incidents_resolved_total",
				Help: "Total incidents resolved in ServiceNow.",
			},
		),
		incidentsSkipped: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "alert2snow_incidents_skipped_total",
				Help: "Total incident creations skipped because an open incident already existed for the correlation ID.",
			},
			[]string{"alertname"},
		),
		alertsFailed: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "alert2snow_alerts_failed_total",
				Help: "Total alerts that could not be processed, by alert status.",
			},
			[]string{"status"},
		),
		serviceNowRequests: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "alert2snow_servicenow_requests_total",
				Help: "Total ServiceNow API calls, by operation and outcome. Counted once per logical call, not once per retry.",
			},
			[]string{"operation", "outcome"},
		),
		serviceNowDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name: "alert2snow_servicenow_request_duration_seconds",
				Help: "End-to-end duration of ServiceNow API calls including retries and backoff.",
				// ServiceNow is a remote SaaS call behind a 30s client timeout;
				// the default buckets top out at 10s and would collapse the slow
				// tail that matters here into +Inf.
				Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 20, 30},
			},
			[]string{"operation"},
		),
	}

	reg.MustRegister(
		m.alertsReceived,
		m.incidentsCreated,
		m.incidentsResolved,
		m.incidentsSkipped,
		m.alertsFailed,
		m.serviceNowRequests,
		m.serviceNowDuration,
	)

	return m
}

// NewNop returns a Metrics registered to a throwaway registry, for tests and
// any caller that needs a valid instance without touching the global one.
func NewNop() *Metrics {
	return New(prometheus.NewRegistry())
}

// AlertReceived records an alert received from Alertmanager.
// status is "firing" or "resolved".
func (m *Metrics) AlertReceived(status string) {
	m.alertsReceived.WithLabelValues(status).Inc()
}

// AlertFailed records an alert that could not be processed.
func (m *Metrics) AlertFailed(status string) {
	m.alertsFailed.WithLabelValues(status).Inc()
}

// IncidentCreated records a newly created ServiceNow incident.
func (m *Metrics) IncidentCreated(alertname string) {
	m.incidentsCreated.WithLabelValues(alertname).Inc()
}

// IncidentResolved records a resolved ServiceNow incident. A single resolved
// alert can resolve more than one incident where pre-dedup duplicates exist, so
// this may be called several times per alert.
func (m *Metrics) IncidentResolved() {
	m.incidentsResolved.Inc()
}

// IncidentSkipped records a suppressed duplicate creation. This is the signal
// that the dedup check is working: it should climb on clusters with sustained
// firing alerts.
func (m *Metrics) IncidentSkipped(alertname string) {
	m.incidentsSkipped.WithLabelValues(alertname).Inc()
}

// ServiceNowRequest records the outcome and duration of a ServiceNow API call.
// Call it once per logical operation, after the retry wrapper returns, so
// retries do not inflate the count.
func (m *Metrics) ServiceNowRequest(operation string, err error, seconds float64) {
	outcome := OutcomeSuccess
	if err != nil {
		outcome = OutcomeError
	}
	m.serviceNowRequests.WithLabelValues(operation, outcome).Inc()
	m.serviceNowDuration.WithLabelValues(operation).Observe(seconds)
}
