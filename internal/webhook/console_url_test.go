package webhook

import (
	"strings"
	"testing"
	"time"

	"github.com/cragr/alert2snow-agent/internal/config"
	"github.com/cragr/alert2snow-agent/internal/models"
)

func TestTransformer_BuildConsoleURL(t *testing.T) {
	tests := []struct {
		name              string
		consoleBaseDomain string
		cluster           string
		namespace         string
		generatorURL      string
		want              string
	}{
		{
			name:         "derives apps domain from GeneratorURL",
			cluster:      "ocp-yk1p1d1",
			namespace:    "openshift-etcd",
			generatorURL: "https://prometheus-k8s-openshift-monitoring.apps.ocp-yk1p1d1.example-corp.cloud/graph?g0.expr=up",
			want:         "https://console-openshift-console.apps.ocp-yk1p1d1.example-corp.cloud/k8s/cluster/projects/openshift-etcd",
		},
		{
			name:              "GeneratorURL wins over configured base domain",
			consoleBaseDomain: "configured.example",
			cluster:           "ocp-yk1p1d1",
			namespace:         "prod",
			generatorURL:      "https://prometheus.apps.real-cluster.real-domain.cloud/graph",
			want:              "https://console-openshift-console.apps.real-cluster.real-domain.cloud/k8s/cluster/projects/prod",
		},
		{
			name:              "falls back to configured base domain when GeneratorURL is absent",
			consoleBaseDomain: "example-corp.cloud",
			cluster:           "ocp-ic1az1d1c1",
			namespace:         "prod",
			generatorURL:      "",
			want:              "https://console-openshift-console.apps.ocp-ic1az1d1c1.example-corp.cloud/k8s/cluster/projects/prod",
		},
		{
			name:              "falls back when GeneratorURL has no apps domain",
			consoleBaseDomain: "example-corp.cloud",
			cluster:           "ocp-ic1az1d1c1",
			namespace:         "prod",
			generatorURL:      "https://localhost:9090/graph?g0.expr=up",
			want:              "https://console-openshift-console.apps.ocp-ic1az1d1c1.example-corp.cloud/k8s/cluster/projects/prod",
		},
		{
			// The D7 contract: no domain means no link, never a guessed one.
			name:         "omits link when no domain can be determined",
			cluster:      "ocp-ic1az1d1c1",
			namespace:    "prod",
			generatorURL: "",
			want:         "",
		},
		{
			name:              "omits link when cluster is unknown and no apps domain",
			consoleBaseDomain: "example-corp.cloud",
			cluster:           "",
			namespace:         "prod",
			generatorURL:      "",
			want:              "",
		},
		{
			// A missing cluster label must not cost the link when the URL has it.
			name:         "no cluster label still yields a link from GeneratorURL",
			cluster:      "",
			namespace:    "prod",
			generatorURL: "https://prometheus.apps.derived.example.cloud/graph",
			want:         "https://console-openshift-console.apps.derived.example.cloud/k8s/cluster/projects/prod",
		},
		{
			name:         "escapes namespace",
			cluster:      "c1",
			namespace:    "weird ns/../etc",
			generatorURL: "https://prometheus.apps.c1.example.cloud/graph",
			want:         "https://console-openshift-console.apps.c1.example.cloud/k8s/cluster/projects/weird%20ns%2F..%2Fetc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := NewTransformer(&config.Config{ConsoleBaseDomain: tt.consoleBaseDomain})
			if got := tr.buildConsoleURL(tt.cluster, tt.namespace, tt.generatorURL); got != tt.want {
				t.Errorf("buildConsoleURL() =\n  %q\nwant\n  %q", got, tt.want)
			}
		})
	}
}

// TestTransformer_NoExampleDotComInDescription guards against the original D7
// defect regressing: a placeholder domain reaching a real incident description.
func TestTransformer_NoExampleDotComInDescription(t *testing.T) {
	tr := NewTransformer(&config.Config{
		ClusterLabelKey:     "cluster",
		EnvironmentLabelKey: "environment",
	})

	incident := tr.Transform(models.Alert{
		Status: "firing",
		Labels: map[string]string{
			"alertname": "KubePodCrashLooping",
			"cluster":   "ocp-yk1p1d1",
			"namespace": "prod",
			"severity":  "critical",
		},
		StartsAt:     time.Now(),
		GeneratorURL: "",
	}, "https://alertmanager.example/#/alerts")

	if strings.Contains(incident.Description, "example.com") {
		t.Errorf("description contains placeholder domain example.com:\n%s", incident.Description)
	}
	// With no GeneratorURL and no ConsoleBaseDomain there is no derivable domain,
	// so the link must be absent rather than dead.
	if strings.Contains(incident.Description, "OpenShift Console:") {
		t.Errorf("description contains a console link with no derivable domain:\n%s", incident.Description)
	}
}
