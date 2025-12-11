// Package webhook handles Alertmanager webhook HTTP requests.
package webhook

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/cragr/alert2snow-agent/internal/config"
	"github.com/cragr/alert2snow-agent/internal/models"
)

// Transformer converts Alertmanager alerts to ServiceNow incidents.
type Transformer struct {
	cfg *config.Config
}

// NewTransformer creates a new Transformer with the given configuration.
func NewTransformer(cfg *config.Config) *Transformer {
	return &Transformer{cfg: cfg}
}

// Transform converts an Alertmanager alert to a ServiceNow incident payload.
func (t *Transformer) Transform(alert models.Alert, externalURL string) models.ServiceNowIncident {
	alertname := alert.Labels["alertname"]
	cluster := t.extractClusterName(alert)
	namespace := alert.Labels["namespace"]
	pod := alert.Labels["pod"]
	container := alert.Labels["container"]
	severity := alert.Labels["severity"]
	environment := alert.Labels[t.cfg.EnvironmentLabelKey]

	shortDesc := t.buildShortDescription(cluster, alertname, namespace)
	description := t.buildDescription(alert, cluster, environment, severity, namespace, pod, container)
	correlationID := GenerateCorrelationID(alertname, alert.Labels)

	return models.ServiceNowIncident{
		ShortDescription: shortDesc,
		Description:      description,
		Impact:           t.cfg.ServiceNowImpact,
		Urgency:          t.cfg.ServiceNowUrgency,
		Category:         t.cfg.ServiceNowCategory,
		Subcategory:      t.cfg.ServiceNowSubcategory,
		AssignmentGroup:  t.cfg.ServiceNowAssignmentGroup,
		CallerID:         t.cfg.ServiceNowCallerID,
		CorrelationID:    correlationID,
	}
}

// buildShortDescription creates the short_description field for ServiceNow.
func (t *Transformer) buildShortDescription(cluster, alertname, namespace string) string {
	if cluster == "" {
		cluster = "unknown-cluster"
	}
	if namespace != "" {
		return fmt.Sprintf("[%s] %s in namespace: %s", cluster, alertname, namespace)
	}
	return fmt.Sprintf("[%s] %s", cluster, alertname)
}

// extractClusterName determines the cluster name from alert labels or GeneratorURL.
// It first checks the configured ClusterLabelKey, then attempts to extract
// the cluster name from the GeneratorURL hostname (apps.<cluster>.<domain> pattern).
func (t *Transformer) extractClusterName(alert models.Alert) string {
	// First, try the configured label
	if cluster := alert.Labels[t.cfg.ClusterLabelKey]; cluster != "" {
		return cluster
	}

	// Fallback: extract from GeneratorURL (OpenShift pattern: apps.<cluster>.<domain>)
	if alert.GeneratorURL != "" {
		if cluster := extractClusterFromURL(alert.GeneratorURL); cluster != "" {
			return cluster
		}
	}

	return ""
}

// extractClusterFromURL extracts the cluster name from an OpenShift-style URL.
// Expected pattern: https://<app>.apps.<cluster>.<domain>/...
// Returns empty string if pattern doesn't match.
func extractClusterFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	host := parsed.Hostname()
	// Look for ".apps." in the hostname
	appsIdx := strings.Index(host, ".apps.")
	if appsIdx == -1 {
		return ""
	}

	// Extract everything after ".apps."
	afterApps := host[appsIdx+6:] // len(".apps.") == 6

	// The cluster name is the first segment before the next dot
	dotIdx := strings.Index(afterApps, ".")
	if dotIdx == -1 {
		return afterApps // No more dots, entire remainder is cluster name
	}

	return afterApps[:dotIdx]
}

// buildDescription creates the detailed description field for ServiceNow.
func (t *Transformer) buildDescription(alert models.Alert, cluster, environment, severity, namespace, pod, container string) string {
	var b strings.Builder

	// Header section
	b.WriteString(fmt.Sprintf("Alert: %s\n", alert.Labels["alertname"]))
	b.WriteString(fmt.Sprintf("Cluster: %s\n", cluster))
	b.WriteString(fmt.Sprintf("Environment: %s\n", environment))
	b.WriteString(fmt.Sprintf("Severity: %s\n", severity))
	b.WriteString(fmt.Sprintf("Started At: %s\n", alert.StartsAt.UTC().Format("2006-01-02 15:04:05 UTC")))

	// Summary section
	if summary := alert.Annotations["summary"]; summary != "" {
		b.WriteString(fmt.Sprintf("\nSummary:\n%s\n", summary))
	}

	// Description section
	if desc := alert.Annotations["description"]; desc != "" {
		b.WriteString(fmt.Sprintf("\nDescription:\n%s\n", desc))
	}

	// Resource information
	if namespace != "" || pod != "" || container != "" {
		b.WriteString("\nResource Information:\n")
		if namespace != "" {
			b.WriteString(fmt.Sprintf("  Namespace: %s\n", namespace))
		}
		if pod != "" {
			b.WriteString(fmt.Sprintf("  Pod: %s\n", pod))
		}
		if container != "" {
			b.WriteString(fmt.Sprintf("  Container: %s\n", container))
		}
	}

	// OpenShift Console link
	if cluster != "" && namespace != "" {
		consoleURL := t.buildConsoleURL(cluster, namespace)
		b.WriteString(fmt.Sprintf("\nOpenShift Console: %s\n", consoleURL))
	}

	// Prometheus link
	if alert.GeneratorURL != "" {
		b.WriteString(fmt.Sprintf("\nPrometheus Link: %s\n", alert.GeneratorURL))
	}

	// All labels
	b.WriteString("\nAll Labels:\n")
	keys := make([]string, 0, len(alert.Labels))
	for k := range alert.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(fmt.Sprintf("  %s: %s\n", k, alert.Labels[k]))
	}

	return b.String()
}

// buildConsoleURL generates an OpenShift console URL for the namespace.
func (t *Transformer) buildConsoleURL(cluster, namespace string) string {
	// Extract base domain from cluster name or use a standard pattern
	return fmt.Sprintf("https://console-openshift-console.apps.%s.example.com/k8s/cluster/projects/%s",
		url.PathEscape(cluster), url.PathEscape(namespace))
}

// GenerateCorrelationID creates a deterministic correlation ID from alert data.
// This ensures the same alert always produces the same ID across multiple replicas.
func GenerateCorrelationID(alertname string, labels map[string]string) string {
	// Sort label keys for deterministic output
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build canonical string: alertname + sorted key-value pairs
	var b strings.Builder
	b.WriteString(alertname)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(labels[k])
	}

	// SHA256 hash, truncate to 16 hex chars (8 bytes)
	hash := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(hash[:8])
}
