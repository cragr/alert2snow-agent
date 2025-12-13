# Alert2Snow Agent

Alertmanager to ServiceNow Incident Bridge - A Go application that receives Prometheus Alertmanager webhooks and creates/resolves incidents in ServiceNow.

## Features

- Receives Prometheus Alertmanager webhook payloads
- Creates incidents in ServiceNow for firing alerts
- Resolves incidents for resolved alerts using correlation ID
- Stateless design for horizontal scaling
- Deterministic correlation ID for deduplication across replicas
- Structured JSON logging
- Prometheus metrics endpoint
- Health and readiness probes for Kubernetes
- Helm chart for OpenShift deployment

## Requirements

- **Helm CLI** - For deploying the application via Helm chart
- **oc** (OpenShift) or **kubectl** (Kubernetes) - For cluster access and management
- ServiceNow instance with Table API access
- OpenShift/Kubernetes cluster for deployment

> **Note:** Go 1.23+ is only required if building the application from source. Pre-built container images are available for direct deployment.

## OpenShift Quickstart

Deploy alert2snow-agent to OpenShift in minutes:

1. **Clone the repository:**
   ```bash
   git clone https://github.com/cragr/alert2snow-agent.git
   cd alert2snow-agent
   ```

2. **Log in to your OpenShift cluster:**
   ```bash
   oc login --server=https://api.your-cluster.example.com:6443
   ```

   Or for generic Kubernetes:
   ```bash
   kubectl config use-context your-cluster-context
   ```

3. **Create a new OpenShift project:**
   ```bash
   oc new-project alert2snow-agent
   ```

4. **Install with Helm:**
   ```bash
   helm install alert2snow ./helm/alert2snow-agent \
     --namespace alert2snow-agent \
     --set servicenow.baseUrl=https://your-instance.service-now.com \
     --set servicenow.username=your-username \
     --set-string servicenow.password=your-password \
     --set servicenow.category=software \
     --set servicenow.subcategory=openshift \
     --set servicenow.assignmentGroup=your-group-sys-id \
     --set servicenow.callerId=your-caller-sys-id
   ```

5. **Verify the deployment:**
   ```bash
   oc get pods -n alert2snow-agent
   ```

6. **Test with a firing alert:**
   ```bash
   oc exec -n alert2snow-agent deploy/alert2snow-alert2snow-agent -- \
     curl -s -X POST http://localhost:8080/alertmanager/webhook \
     -H "Content-Type: application/json" \
     -d @- <<'EOF'
   {"version":"4","status":"firing","alerts":[{"status":"firing","labels":{"alertname":"TestAlert","severity":"critical"}}]}
   EOF
   ```

   Or use the included test payload file:
   ```bash
   curl -X POST http://alert2snow-alert2snow-agent.alert2snow-agent.svc.cluster.local:8080/alertmanager/webhook \
     -H "Content-Type: application/json" \
     -d @test-payload.json
   ```

7. **Test with a resolved alert:**
   ```bash
   oc exec -n alert2snow-agent deploy/alert2snow-alert2snow-agent -- \
     curl -s -X POST http://localhost:8080/alertmanager/webhook \
     -H "Content-Type: application/json" \
     -d @- <<'EOF'
   {"version":"4","status":"resolved","alerts":[{"status":"resolved","labels":{"alertname":"TestAlert","severity":"critical"}}]}
   EOF
   ```

   Or use the included test payload file:
   ```bash
   curl -X POST http://alert2snow-alert2snow-agent.alert2snow-agent.svc.cluster.local:8080/alertmanager/webhook \
     -H "Content-Type: application/json" \
     -d @test-payload-resolved.json
   ```

8. **Check pod logs for troubleshooting:**
   ```bash
   oc logs -f deploy/alert2snow-alert2snow-agent -n alert2snow-agent
   ```

9. **Configure Alertmanager to route alerts:**

   Update the Alertmanager configuration by editing the `alertmanager-main` secret in the `openshift-monitoring` namespace:

   ```bash
   oc -n openshift-monitoring edit secret alertmanager-main
   ```

   The secret contains a base64-encoded `alertmanager.yaml` key. Decode it, add the receiver configuration from the [Alertmanager Configuration](#alertmanager-configuration) section, then re-encode and save.

   Alternatively, extract, edit, and apply:
   ```bash
   # Extract current config
   oc -n openshift-monitoring get secret alertmanager-main -o jsonpath='{.data.alertmanager\.yaml}' | base64 -d > alertmanager.yaml

   # Edit alertmanager.yaml to add the servicenow-bridge receiver and route
   # (See Alertmanager Configuration section below for the snippet to add)

   # Apply updated config
   oc -n openshift-monitoring set data secret/alertmanager-main --from-file=alertmanager.yaml
   ```

   After saving, the Alertmanager pods will automatically reload the configuration once the secret update propagates.

For detailed configuration options, see [Helm Deployment](#helm-deployment) and [Configuration](#configuration).

## Local Development Quick Start

### Build

```bash
go build -o alert2snow-agent ./cmd/app
```

### Run Tests

```bash
go test ./...
```

### Run Locally

```bash
export SERVICENOW_BASE_URL=https://your-instance.service-now.com
export SERVICENOW_ENDPOINT_PATH=/api/now/table/incident
export SERVICENOW_USERNAME=your-username
export SERVICENOW_PASSWORD=your-password
./alert2snow-agent
```

### Test Webhook (Firing Alert)

```bash
curl -X POST http://localhost:8080/alertmanager/webhook \
  -H "Content-Type: application/json" \
  -d @test-payload.json
```

### Test Webhook (Resolved Alert)

```bash
curl -X POST http://localhost:8080/alertmanager/webhook \
  -H "Content-Type: application/json" \
  -d @test-payload-resolved.json
```

## Configuration

| Environment Variable | Required | Default | Description |
|---------------------|----------|---------|-------------|
| `SERVICENOW_BASE_URL` | Yes | - | ServiceNow instance URL |
| `SERVICENOW_ENDPOINT_PATH` | No | `/api/now/table/incident` | Table API path |
| `SERVICENOW_USERNAME` | Yes | - | ServiceNow username |
| `SERVICENOW_PASSWORD` | Yes | - | ServiceNow password |
| `SERVICENOW_CATEGORY` | No | `software` | Incident category |
| `SERVICENOW_SUBCATEGORY` | No | `openshift` | Incident subcategory |
| `SERVICENOW_ASSIGNMENT_GROUP` | No | - | Assignment group sys_id or name |
| `SERVICENOW_CALLER_ID` | No | - | Caller sys_id or user_name |
| `HTTP_PORT` | No | `8080` | HTTP server port |
| `CLUSTER_LABEL_KEY` | No | `cluster` | Alert label for cluster name |
| `ENVIRONMENT_LABEL_KEY` | No | `environment` | Alert label for environment |

## Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/alertmanager/webhook` | POST | Receive Alertmanager webhooks |
| `/healthz` | GET | Liveness probe |
| `/readyz` | GET | Readiness probe |
| `/metrics` | GET | Prometheus metrics |

## Container Build

### Native Build (same architecture)

```bash
podman build -t alert2snow-agent:latest .
```

### Cross-Architecture Build (Apple Silicon → amd64)

When building on Apple Silicon (arm64) for deployment on amd64 OpenShift clusters:

```bash
podman build --platform linux/amd64 -t alert2snow-agent:latest .
```

**Notes for Apple Silicon users:**
- The `--platform linux/amd64` flag is required to produce amd64 images
- Cross-architecture builds use QEMU emulation and are slower than native builds
- The Containerfile requires no modifications for cross-architecture builds
- Ensure your Podman machine has sufficient resources allocated for emulated builds

To verify the image architecture:

```bash
podman inspect alert2snow-agent:latest --format '{{.Architecture}}'
```

## Helm Deployment

### Install

```bash
helm install alert2snow ./helm/alert2snow-agent \
  --namespace monitoring \
  --set servicenow.baseUrl=https://your-instance.service-now.com \
  --set servicenow.username=your-username \
  --set-string servicenow.password=your-password
```

With optional ServiceNow field customization:

```bash
helm install alert2snow ./helm/alert2snow-agent \
  --namespace monitoring \
  --set servicenow.baseUrl=https://your-instance.service-now.com \
  --set servicenow.username=your-username \
  --set-string servicenow.password=your-password \
  --set servicenow.category=software \
  --set servicenow.subcategory=openshift \
  --set servicenow.assignmentGroup=your-group-sys-id \
  --set servicenow.callerId=your-caller-sys-id
```

### Helm Values Reference

| Value | Default | Description |
|-------|---------|-------------|
| `servicenow.baseUrl` | `""` | ServiceNow instance URL (required) |
| `servicenow.endpointPath` | `/api/now/table/incident` | Table API path |
| `servicenow.username` | `""` | ServiceNow username (required) |
| `servicenow.password` | `""` | ServiceNow password (required) |
| `servicenow.category` | `software` | Incident category |
| `servicenow.subcategory` | `openshift` | Incident subcategory |
| `servicenow.assignmentGroup` | `""` | Assignment group (optional) |
| `servicenow.callerId` | `""` | Caller ID (optional) |
| `config.httpPort` | `8080` | HTTP server port |
| `config.clusterLabelKey` | `cluster` | Alert label for cluster name |
| `config.environmentLabelKey` | `environment` | Alert label for environment |

### Upgrade

```bash
helm upgrade alert2snow ./helm/alert2snow-agent \
  --namespace monitoring \
  --reuse-values
```

### Uninstall

```bash
helm uninstall alert2snow --namespace monitoring
```

## Alertmanager Configuration

### Recommended Configuration

Add the following to your Alertmanager configuration to route critical alerts to ServiceNow while minimizing ticket spam:

```yaml
global:
  resolve_timeout: 5m
  http_config:
    proxy_from_environment: true

# Inhibition rules - suppress child alerts when parent infrastructure alerts fire
inhibit_rules:
  # When a node is down, suppress pod-level alerts on that node
  - source_matchers:
      - 'alertname="KubeNodeNotReady"'
    target_matchers:
      - 'alertname=~"KubePod.*"'
    equal:
      - node
  # When a namespace is terminating, suppress alerts from that namespace
  - source_matchers:
      - 'alertname="KubeNamespaceTerminating"'
    target_matchers:
      - 'severity="critical"'
    equal:
      - namespace

route:
  group_wait: 30s
  group_interval: 5m
  repeat_interval: 12h
  receiver: default
  routes:
    # Watchdog heartbeat - high-frequency ping for dead man's switch monitoring
    - matchers:
        - 'alertname="Watchdog"'
      repeat_interval: 2m
      receiver: watchdog

    # Critical alerts to ServiceNow (with exclusions for non-actionable alerts)
    - matchers:
        - 'severity="critical"'
        - 'alertname!~"^(PodDisruptionBudgetLimit|InfoInhibitor|AlertmanagerReceiversNotConfigured)$"'
      receiver: servicenow-bridge
      group_by:
        - alertname
        - namespace
      group_wait: 30s
      group_interval: 5m
      repeat_interval: 4h
      continue: true  # Also send to default receiver for backup/audit logging

receivers:
  - name: default
  - name: watchdog
  - name: servicenow-bridge
    webhook_configs:
      - url: 'http://alert2snow-alert2snow-agent.alert2snow-agent.svc.cluster.local:8080/alertmanager/webhook'
        send_resolved: true
        max_alerts: 50
        http_config:
          proxy_from_environment: true
          tls_config:
            insecure_skip_verify: false
```

### Configuration Explained

| Setting | Value | Purpose |
|---------|-------|---------|
| `group_by` | `['alertname', 'cluster', 'namespace']` | Groups related alerts into a single notification, reducing duplicate tickets |
| `group_wait` | `30s` | Waits 30 seconds to batch alerts before sending first notification |
| `group_interval` | `5m` | Waits 5 minutes before sending updates for an existing alert group |
| `repeat_interval` | `4h` | Re-sends alerts every 4 hours if still firing (prevents ticket storms from flapping) |
| `send_resolved: true` | - | Sends resolution notifications so incidents auto-close |

### Alert Exclusions

The configuration excludes these alerts from ServiceNow:

- **`PodDisruptionBudgetLimit`** - Often fires during normal maintenance operations
- **`Watchdog`** - A heartbeat alert that fires continuously by design

To exclude additional alerts, add more matchers:
```yaml
matchers:
  - severity="critical"
  - alertname!="PodDisruptionBudgetLimit"
  - alertname!="Watchdog"
  - alertname!="YourAlertToExclude"
```

### Inhibition Rules

The optional `inhibit_rules` section suppresses warning/info alerts when a critical alert is already firing for the same resource. This prevents related lower-severity alerts from creating duplicate tickets.

> **Note:** Adjust the namespace in the URL if you deployed to a different namespace than `alert2snow-agent`.

## Testing Alerts Through Alertmanager

### Option 1: Using amtool (Recommended)

The `amtool` CLI can send test alerts directly to Alertmanager. From a pod with access to Alertmanager:

```bash
# Send a test critical alert (should create ServiceNow incident)
amtool alert add TestServiceNowAlert \
  severity="critical" \
  cluster="test-cluster" \
  namespace="test-namespace" \
  --annotation=summary="Test alert for ServiceNow integration" \
  --annotation=description="This is a test alert to verify the alert2snow-agent integration." \
  --alertmanager.url=http://alertmanager-main.openshift-monitoring.svc:9093

# Verify alert is active
amtool alert query --alertmanager.url=http://alertmanager-main.openshift-monitoring.svc:9093

# Resolve the alert (should close ServiceNow incident)
amtool alert add TestServiceNowAlert \
  severity="critical" \
  cluster="test-cluster" \
  namespace="test-namespace" \
  --end=$(date -u +%Y-%m-%dT%H:%M:%SZ) \
  --alertmanager.url=http://alertmanager-main.openshift-monitoring.svc:9093
```

### Option 2: Using curl to Alertmanager API

Send alerts directly to the Alertmanager API:

```bash
# Send a test critical alert
curl -X POST http://alertmanager-main.openshift-monitoring.svc:9093/api/v2/alerts \
  -H "Content-Type: application/json" \
  -d '[{
    "labels": {
      "alertname": "TestServiceNowAlert",
      "severity": "critical",
      "cluster": "test-cluster",
      "namespace": "test-namespace"
    },
    "annotations": {
      "summary": "Test alert for ServiceNow integration",
      "description": "This is a test alert to verify the alert2snow-agent integration."
    },
    "startsAt": "'$(date -u +%Y-%m-%dT%H:%M:%SZ)'"
  }]'
```

### Option 3: PrometheusRule for Persistent Testing

Create a PrometheusRule that fires when a specific condition is met:

```yaml
apiVersion: monitoring.coreos.com/v1
kind: PrometheusRule
metadata:
  name: test-servicenow-alert
  namespace: openshift-monitoring
spec:
  groups:
    - name: servicenow-test
      rules:
        - alert: TestServiceNowIntegration
          # Fires when the test ConfigMap exists
          expr: kube_configmap_info{configmap="trigger-servicenow-test", namespace="openshift-monitoring"} > 0
          for: 1m
          labels:
            severity: critical
            cluster: "test-cluster"
          annotations:
            summary: "Test alert for ServiceNow integration"
            description: "This alert fires when the trigger-servicenow-test ConfigMap exists."
```

To trigger:
```bash
# Create trigger ConfigMap (alert fires after 1 minute)
oc create configmap trigger-servicenow-test -n openshift-monitoring

# Remove to resolve
oc delete configmap trigger-servicenow-test -n openshift-monitoring
```

### Verifying Alert Flow

1. **Watch alert2snow-agent logs:**
   ```bash
   oc logs -f deploy/alert2snow-alert2snow-agent -n alert2snow-agent
   ```

2. **Expected log output for firing alert:**
   ```
   {"level":"info","msg":"received alertmanager webhook","alert_count":1}
   {"level":"info","msg":"processing alert","alertname":"TestServiceNowAlert","status":"firing"}
   {"level":"info","msg":"created incident","incident_number":"INC0012345"}
   ```

3. **Expected log output for resolved alert:**
   ```
   {"level":"info","msg":"processing alert","alertname":"TestServiceNowAlert","status":"resolved"}
   {"level":"info","msg":"resolved incident","sys_id":"..."}
   ```

### Testing Exclusions

Verify that excluded alerts do NOT create tickets:

```bash
# Send a PodDisruptionBudgetLimit alert (should be ignored)
amtool alert add PodDisruptionBudgetLimit \
  severity="critical" \
  cluster="test-cluster" \
  namespace="test-namespace" \
  --alertmanager.url=http://alertmanager-main.openshift-monitoring.svc:9093
```

Check the agent logs - you should see the webhook received but NO incident created because Alertmanager's route excludes this alert before it reaches the webhook.

> **Note:** If the exclusion is working correctly, the alert2snow-agent will never receive the `PodDisruptionBudgetLimit` alert - it's filtered at the Alertmanager routing level.

## Architecture

```
┌──────────────────┐     ┌──────────────────┐     ┌──────────────────┐
│   Alertmanager   │────▶│  alert2snow-agent│────▶│    ServiceNow    │
│                  │     │                  │     │                  │
│  firing/resolved │     │  Transform alert │     │  Create/Resolve  │
│     webhooks     │     │  to incident     │     │    Incidents     │
└──────────────────┘     └──────────────────┘     └──────────────────┘
```

### Correlation Strategy

The agent uses a deterministic correlation ID generated from:
- Alert name
- Sorted label key-value pairs

This ensures:
- Same alert always generates the same correlation ID
- Multiple replicas can process alerts without conflicts
- Resolved alerts can find and update their corresponding incidents

## Development

### Project Structure

```
.
├── cmd/app/                    # Application entrypoint
├── internal/
│   ├── config/                 # Configuration loading
│   ├── logging/                # Structured logging
│   ├── models/                 # Data types
│   ├── servicenow/             # ServiceNow API client
│   └── webhook/                # HTTP handler and transformer
├── helm/alert2snow-agent/      # Helm chart
├── Containerfile               # Container build (Podman/Docker compatible)
├── test-payload.json           # Sample Alertmanager payload (firing)
├── test-payload-resolved.json  # Sample Alertmanager payload (resolved)
└── example-servicenow-payload.json  # Expected ServiceNow format
```

### Running Tests

```bash
# All tests
go test ./...

# With coverage
go test -cover ./...

# Specific package
go test ./internal/webhook/

# Specific test
go test -run TestTransformAlert ./internal/webhook/
```

## Repository Setup

### Clone the Repository

```bash
git clone https://github.com/cragr/alert2snow-agent.git
cd alert2snow-agent
```

### Install Dependencies

```bash
go mod download
```

### Run Tests

```bash
go test ./...
```

### Build the Application

```bash
go build -o alert2snow-agent ./cmd/app
```

### Build the Container

Native build (same architecture as host):

```bash
podman build -t alert2snow-agent:latest .
```

Cross-architecture build (Apple Silicon → amd64 for OpenShift):

```bash
podman build --platform linux/amd64 -t alert2snow-agent:latest .
```

Or with Docker (uses the same Containerfile):

```bash
docker build -f Containerfile -t alert2snow-agent:latest .
# For cross-arch: docker build --platform linux/amd64 -f Containerfile -t alert2snow-agent:latest .
```

### Run Locally

Set required environment variables and start the application:

```bash
export SERVICENOW_BASE_URL=https://your-instance.service-now.com
export SERVICENOW_USERNAME=your-username
export SERVICENOW_PASSWORD=your-password
./alert2snow-agent
```

### Working with Branches and PRs

1. Create a feature branch from `main`:
   ```bash
   git checkout -b feature/your-feature-name
   ```

2. Make your changes and run tests:
   ```bash
   go test ./...
   ```

3. Commit your changes:
   ```bash
   git add .
   git commit -m "Description of changes"
   ```

4. Push to your fork and create a pull request:
   ```bash
   git push origin feature/your-feature-name
   ```

See [CONTRIBUTING.md](CONTRIBUTING.md) for detailed contribution guidelines.

## License

MIT - See [LICENSE](LICENSE) for details.
