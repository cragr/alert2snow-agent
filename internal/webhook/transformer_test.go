package webhook

import (
	"strings"
	"testing"
	"time"

	"github.com/cragr/alert2snow-agent/internal/config"
	"github.com/cragr/alert2snow-agent/internal/models"
)

func TestGenerateCorrelationID(t *testing.T) {
	tests := []struct {
		name      string
		alertname string
		labels    map[string]string
		wantLen   int
	}{
		{
			name:      "basic alert",
			alertname: "TestAlert",
			labels: map[string]string{
				"alertname": "TestAlert",
				"severity":  "warning",
			},
			wantLen: 16,
		},
		{
			name:      "labels order should not matter",
			alertname: "TestAlert",
			labels: map[string]string{
				"severity":  "warning",
				"alertname": "TestAlert",
				"namespace": "default",
			},
			wantLen: 16,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateCorrelationID(tt.alertname, tt.labels)
			if len(got) != tt.wantLen {
				t.Errorf("GenerateCorrelationID() length = %v, want %v", len(got), tt.wantLen)
			}
		})
	}
}

func TestGenerateCorrelationID_Deterministic(t *testing.T) {
	labels := map[string]string{
		"alertname": "TestAlert",
		"severity":  "warning",
		"namespace": "openshift-monitoring",
		"pod":       "prometheus-k8s-0",
	}

	id1 := GenerateCorrelationID("TestAlert", labels)
	id2 := GenerateCorrelationID("TestAlert", labels)

	if id1 != id2 {
		t.Errorf("GenerateCorrelationID() not deterministic: %v != %v", id1, id2)
	}
}

func TestGenerateCorrelationID_DifferentAlerts(t *testing.T) {
	labels1 := map[string]string{"alertname": "Alert1", "severity": "warning"}
	labels2 := map[string]string{"alertname": "Alert2", "severity": "warning"}

	id1 := GenerateCorrelationID("Alert1", labels1)
	id2 := GenerateCorrelationID("Alert2", labels2)

	if id1 == id2 {
		t.Error("GenerateCorrelationID() should produce different IDs for different alerts")
	}
}

func TestTransformer_Transform(t *testing.T) {
	cfg := &config.Config{
		ClusterLabelKey:       "cluster",
		EnvironmentLabelKey:   "environment",
		ServiceNowCategory:    "software",
		ServiceNowSubcategory: "openshift",
		ServiceNowUrgency:     "3",
		ServiceNowImpact:      "3",
	}
	transformer := NewTransformer(cfg)

	alert := models.Alert{
		Status: "firing",
		Labels: map[string]string{
			"alertname":   "KubePodCrashLooping",
			"cluster":     "production-cluster",
			"environment": "prod",
			"severity":    "warning",
			"namespace":   "openshift-monitoring",
			"pod":         "prometheus-k8s-0",
			"container":   "prometheus",
		},
		Annotations: map[string]string{
			"summary":     "Pod is crash looping",
			"description": "Pod prometheus-k8s-0 is restarting frequently.",
		},
		StartsAt:     time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
		GeneratorURL: "http://prometheus/graph?g0.expr=up",
		Fingerprint:  "abc123",
	}

	incident := transformer.Transform(alert, "http://alertmanager")

	// Check short description
	expectedShortDesc := "[production-cluster] KubePodCrashLooping in namespace: openshift-monitoring"
	if incident.ShortDescription != expectedShortDesc {
		t.Errorf("ShortDescription = %q, want %q", incident.ShortDescription, expectedShortDesc)
	}

	// Check impact and urgency (should always be 3)
	if incident.Impact != "3" {
		t.Errorf("Impact = %q, want %q", incident.Impact, "3")
	}
	if incident.Urgency != "3" {
		t.Errorf("Urgency = %q, want %q", incident.Urgency, "3")
	}

	// Check category
	if incident.Category != "software" {
		t.Errorf("Category = %q, want %q", incident.Category, "software")
	}
	if incident.Subcategory != "openshift" {
		t.Errorf("Subcategory = %q, want %q", incident.Subcategory, "openshift")
	}

	// Check correlation ID is generated
	if incident.CorrelationID == "" {
		t.Error("CorrelationID should not be empty")
	}
	if len(incident.CorrelationID) != 16 {
		t.Errorf("CorrelationID length = %d, want 16", len(incident.CorrelationID))
	}

	// Check description contains expected content
	if !strings.Contains(incident.Description, "Alert: KubePodCrashLooping") {
		t.Error("Description should contain alert name")
	}
	if !strings.Contains(incident.Description, "Cluster: production-cluster") {
		t.Error("Description should contain cluster")
	}
	if !strings.Contains(incident.Description, "Pod is crash looping") {
		t.Error("Description should contain summary")
	}
	if !strings.Contains(incident.Description, "Namespace: openshift-monitoring") {
		t.Error("Description should contain namespace")
	}
}

func TestTransformer_Transform_MissingCluster(t *testing.T) {
	cfg := &config.Config{
		ClusterLabelKey:       "cluster",
		EnvironmentLabelKey:   "environment",
		ServiceNowCategory:    "software",
		ServiceNowSubcategory: "openshift",
		ServiceNowUrgency:     "3",
		ServiceNowImpact:      "3",
	}
	transformer := NewTransformer(cfg)

	alert := models.Alert{
		Status: "firing",
		Labels: map[string]string{
			"alertname": "TestAlert",
			"severity":  "warning",
		},
		Annotations: map[string]string{},
		StartsAt:    time.Now(),
	}

	incident := transformer.Transform(alert, "")

	expectedShortDesc := "[unknown-cluster] TestAlert"
	if incident.ShortDescription != expectedShortDesc {
		t.Errorf("ShortDescription = %q, want %q", incident.ShortDescription, expectedShortDesc)
	}
}

func TestExtractClusterFromURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "standard OpenShift console URL",
			url:      "https://console-openshift-console.apps.os-lb3az1d1.ssnc-corp.cloud/monitoring/alerts",
			expected: "os-lb3az1d1",
		},
		{
			name:     "Prometheus URL with apps pattern",
			url:      "https://prometheus-k8s.apps.my-cluster.example.com/graph?g0.expr=up",
			expected: "my-cluster",
		},
		{
			name:     "URL without apps pattern",
			url:      "https://prometheus.example.com/graph",
			expected: "",
		},
		{
			name:     "invalid URL",
			url:      "not-a-url",
			expected: "",
		},
		{
			name:     "empty URL",
			url:      "",
			expected: "",
		},
		{
			name:     "cluster name with hyphens and numbers",
			url:      "https://alertmanager.apps.prod-cluster-01.corp.local/api/v2/alerts",
			expected: "prod-cluster-01",
		},
		{
			name:     "simple domain after apps",
			url:      "https://app.apps.testcluster.io/",
			expected: "testcluster",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractClusterFromURL(tt.url)
			if got != tt.expected {
				t.Errorf("extractClusterFromURL(%q) = %q, want %q", tt.url, got, tt.expected)
			}
		})
	}
}

func TestTransformer_ExtractClusterName_FromURL(t *testing.T) {
	cfg := &config.Config{
		ClusterLabelKey:       "cluster",
		EnvironmentLabelKey:   "environment",
		ServiceNowCategory:    "software",
		ServiceNowSubcategory: "openshift",
		ServiceNowUrgency:     "3",
		ServiceNowImpact:      "3",
	}
	transformer := NewTransformer(cfg)

	// Alert without cluster label but with GeneratorURL containing cluster name
	alert := models.Alert{
		Status: "firing",
		Labels: map[string]string{
			"alertname":       "ClusterOperatorDown",
			"managed_cluster": "97a4b324-65bf-425b-bdb7-a1a4c611ee74",
			"namespace":       "openshift-cluster-version",
			"severity":        "critical",
		},
		Annotations: map[string]string{
			"summary": "Cluster operator is down",
		},
		StartsAt:     time.Now(),
		GeneratorURL: "https://console-openshift-console.apps.os-lb3az1d1.ssnc-corp.cloud/monitoring/alerts",
	}

	incident := transformer.Transform(alert, "")

	// Should extract cluster from GeneratorURL
	expectedShortDesc := "[os-lb3az1d1] ClusterOperatorDown in namespace: openshift-cluster-version"
	if incident.ShortDescription != expectedShortDesc {
		t.Errorf("ShortDescription = %q, want %q", incident.ShortDescription, expectedShortDesc)
	}

	// Description should also contain the extracted cluster
	if !strings.Contains(incident.Description, "Cluster: os-lb3az1d1") {
		t.Errorf("Description should contain extracted cluster name, got: %s", incident.Description)
	}
}

func TestTransformer_ExtractClusterName_LabelTakesPrecedence(t *testing.T) {
	cfg := &config.Config{
		ClusterLabelKey:       "cluster",
		EnvironmentLabelKey:   "environment",
		ServiceNowCategory:    "software",
		ServiceNowSubcategory: "openshift",
		ServiceNowUrgency:     "3",
		ServiceNowImpact:      "3",
	}
	transformer := NewTransformer(cfg)

	// Alert with both cluster label AND GeneratorURL - label should take precedence
	alert := models.Alert{
		Status: "firing",
		Labels: map[string]string{
			"alertname": "TestAlert",
			"cluster":   "label-cluster",
			"namespace": "default",
		},
		StartsAt:     time.Now(),
		GeneratorURL: "https://console.apps.url-cluster.example.com/",
	}

	incident := transformer.Transform(alert, "")

	// Should use cluster from label, not URL
	expectedShortDesc := "[label-cluster] TestAlert in namespace: default"
	if incident.ShortDescription != expectedShortDesc {
		t.Errorf("ShortDescription = %q, want %q", incident.ShortDescription, expectedShortDesc)
	}
}
