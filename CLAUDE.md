# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Alertmanager → ServiceNow Incident Bridge: A Go application that receives Prometheus Alertmanager webhooks and creates/resolves incidents in ServiceNow. Runs stateless on OpenShift with horizontal scaling support.

## Build Commands

```bash
# Build
go build -o alert2snow-agent ./cmd/app

# Run tests
go test ./...

# Run single test
go test -run TestName ./package/

# Tidy dependencies
go mod tidy

# Build container
podman build -t alert2snow-agent:latest .

# Local run
export SERVICENOW_BASE_URL=https://instance.service-now.com
export SERVICENOW_ENDPOINT_PATH=/api/now/table/incident
export SERVICENOW_USERNAME=user
export SERVICENOW_PASSWORD=pass
./alert2snow-agent

# Test webhook locally
curl -X POST http://localhost:8080/alertmanager/webhook \
  -H "Content-Type: application/json" \
  -d @test-payload.json
```

## Architecture

```
cmd/app/          # Main entrypoint
config/           # Environment configuration loading
models/           # Data types: Alertmanager, ServiceNow, internal
webhook/          # HTTP handler for /alertmanager/webhook
servicenow/       # ServiceNow API client with retry logic
logging/          # Structured logging utilities
helm/             # Helm chart for OpenShift deployment
```

### Data Flow
1. Alertmanager POSTs to `/alertmanager/webhook`
2. Webhook handler parses payload, extracts alerts
3. For each alert: determine status (firing/resolved), generate correlation_id
4. ServiceNow client creates incident (firing) or resolves existing (resolved)

### Key Design Decisions
- **Correlation ID**: Deterministic hash of alertname + sorted labels for deduplication across replicas
- **Stateless**: No local state; correlation handled via ServiceNow correlation_id field
- **Retry**: Exponential backoff on ServiceNow API failures

## Environment Variables

| Variable | Description |
|----------|-------------|
| `SERVICENOW_BASE_URL` | ServiceNow instance URL |
| `SERVICENOW_ENDPOINT_PATH` | API path (e.g., `/api/now/table/incident`) |
| `SERVICENOW_USERNAME` | Basic auth username |
| `SERVICENOW_PASSWORD` | Basic auth password |
| `HTTP_PORT` | Listen port (default: 8080) |

## Endpoints

- `POST /alertmanager/webhook` - Receive alerts
- `GET /healthz` - Liveness probe
- `GET /readyz` - Readiness probe
- `GET /metrics` - Prometheus metrics

## Sample Payloads

- `test-payload.json` - Sample Alertmanager webhook input
- `example-servicenow-payload.json` - Expected ServiceNow output format

## Helm Deployment

```bash
helm install alert2snow ./helm/alert2snow-agent \
  --set servicenow.baseUrl=https://instance.service-now.com \
  --set servicenow.username=user \
  --set-string servicenow.password=secret
```

### Severity Mapping
| Alert Severity | ServiceNow Impact | ServiceNow Urgency |
|---------------|-------------------|-------------------|
| critical      | 3 - Moderate      | 3 - Moderate      |
| warning       | 3 - Moderate      | 3 - Moderate      |
| info          | 3 - Moderate      | 3 - Moderate      |

## Implementation Requirements

- Go latest stable, idiomatic code with testable interfaces
- Multi-stage Containerfile with UBI9 minimal runtime, non-root user
- Helm chart with Deployment, Service, Secret, ConfigMap
- Structured JSON logging, never log credentials
- Handle multiple alerts per webhook payload
