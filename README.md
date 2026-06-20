# LLM Observatory

<p align="center">
  <img src="./assets/img/observatory.jpg" width="500" alt="LLM Observatory">
</p>

<p align="center">
  <strong>Observability platform for Large Language Model workloads.</strong>
</p>

---

## Overview

LLM Observatory is a lightweight Go API that forwards prompts to a local Ollama instance and exposes operational telemetry for monitoring LLM workloads.

The project already includes:

- a chat API
- structured JSON logs
- Prometheus metrics
- Grafana dashboards
- Loki centralized logs
- OpenTelemetry traces with Tempo
- Docker Compose for local observability

The objective is to make model behavior visible in production-like environments instead of building another chat UI.

---

## Current Architecture

```text
Client
  |
  v
Gin API
  |
  v
Ollama
  |
  v
Qwen or another local model

Metrics  ----------> Prometheus
Dashboards -------> Grafana
Structured logs --> stdout --> Loki --> Grafana
Traces -----------> OpenTelemetry --> Tempo --> Grafana
```

---

## Features

### Chat endpoint

```http
POST /chat
```

Example request:

```json
{
  "prompt": "What is Kubernetes?",
  "model": "qwen2.5:0.5b"
}
```

`model` is optional. If omitted, the API uses the `MODEL` environment variable.

Example response:

```json
{
  "prompt": "What is Kubernetes?",
  "model": "qwen2.5:0.5b",
  "answer": "Kubernetes is...",
  "total_duration_ns": 1234567890,
  "load_duration_ns": 12345678,
  "prompt_eval_count": 14,
  "prompt_eval_duration_ns": 23456789,
  "eval_count": 92,
  "eval_duration_ns": 456789012,
  "tokens_per_sec": 201.4
}
```

### Health endpoints

```http
GET /healthz
GET /readyz
```

- `/healthz` reports whether the API process is up
- `/readyz` checks whether Ollama is reachable

### Prometheus metrics

```http
GET /metrics
```

The API exposes labeled metrics for:

- request totals by endpoint, model and status
- error totals by endpoint, model and error type
- request latency
- generated tokens
- prompt tokens
- model total duration
- model load duration
- model evaluation duration
- token generation speed

### Structured logging

Every request gets a `request_id` and is logged in JSON with:

- HTTP method
- path
- status
- latency
- selected model
- prompt and answer sizes
- upstream Ollama failures

---

## Tech Stack

- Go
- Gin
- Ollama
- Prometheus
- Grafana
- Loki
- Promtail
- OpenTelemetry
- Tempo
- Docker Compose

---

## Getting Started

### Requirements

- Go 1.25+
- Ollama
- A local model pulled in Ollama
- Docker Desktop or Docker Engine with Compose, if you want to run the full observability stack

### Run locally

Start Ollama with a model:

```bash
ollama run qwen2.5:0.5b
```

Start the API:

```bash
go run ./src
```

Available endpoints:

- API: `http://localhost:8080`
- Metrics: `http://localhost:8080/metrics`
- Health: `http://localhost:8080/healthz`
- Readiness: `http://localhost:8080/readyz`

### Run tests

```bash
go test ./...
```

The test suite uses a mocked Ollama HTTP server. Ollama does not need to be running for tests.

### Run with Docker Compose

Start Ollama on the host machine first:

```bash
ollama run qwen2.5:0.5b
```

Then start the stack:

```bash
docker compose up --build
```

Available services:

- API: `http://localhost:8080`
- Prometheus: `http://localhost:9090`
- Grafana: `http://localhost:3000`
- Loki: `http://localhost:3100`
- Tempo: `http://localhost:3200`

The Compose setup uses `host.docker.internal` so the API container can reach the Ollama process running on the host. The `extra_hosts` entry in `docker-compose.yml` makes this work on Linux Docker Engine as well as Docker Desktop on Windows and macOS.

Default Grafana credentials:

- user: `admin`
- password: `admin`

### Portability notes

The application can be launched on Windows, macOS, and Linux as long as these are installed:

- Go 1.25+ for local runs and tests
- Ollama with the configured model pulled locally
- Docker with Compose for the full monitoring stack

Configuration should be supplied through environment variables or a local `.env` file based on `.env.example`. The Docker image does not copy `.env`, so local secrets and machine-specific values are not baked into the image.

---

## Configuration

Environment variables:

| Variable | Default | Description |
| --- | --- | --- |
| `MODEL` | `qwen2.5:0.5b` | Default Ollama model used when `model` is not provided in `/chat` |
| `OLLAMA_URL` | `http://localhost:11434/api/chat` | Ollama chat endpoint |
| `OLLAMA_HEALTH_URL` | derived from `OLLAMA_URL` as `/api/tags` | Ollama readiness endpoint |
| `OLLAMA_TIMEOUT_SECONDS` | `60` | Timeout used for upstream HTTP calls |
| `OTEL_SERVICE_NAME` | `llm-observatory-api` | Service name attached to OpenTelemetry traces |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `tempo:4318` in Docker Compose, `localhost:4318` locally | OTLP HTTP endpoint used to export traces |

Example `.env.example`:

```env
OLLAMA_URL=http://host.docker.internal:11434/api/chat
OLLAMA_HEALTH_URL=http://host.docker.internal:11434/api/tags
OLLAMA_TIMEOUT_SECONDS=60
MODEL=qwen2.5:0.5b
OTEL_SERVICE_NAME=llm-observatory-api
OTEL_EXPORTER_OTLP_ENDPOINT=localhost:4318
```

---

## Observability

### Prometheus

Prometheus scrapes the API service through Docker networking using:

```yaml
targets: ["api:8080"]
```

### Grafana dashboard

The provided dashboard includes:

- requests per second
- error rate
- latency percentiles
- total requests
- generated tokens
- prompt tokens
- token throughput
- generation speed
- model load time
- model comparison panels
- centralized application logs

### Loki logs

Promtail discovers Docker containers and forwards their stdout logs to Loki.
The API emits JSON logs with `request_id` and `trace_id`, allowing logs to be correlated with Tempo traces in Grafana.

### Tempo traces

The API uses OpenTelemetry instrumentation for:

- inbound Gin requests
- outbound HTTP calls to Ollama
- context propagation with W3C trace headers

Tempo receives OTLP HTTP traces on port `4318` and exposes them to Grafana on port `3200`.

---

## Roadmap

### Phase 1 - Core API

- [x] Go API
- [x] Ollama integration
- [x] Local inference
- [x] Structured logging
- [x] Health endpoints
- [x] Request correlation identifiers

### Phase 2 - Metrics

- [x] Prometheus integration
- [x] Request counter
- [x] Latency metrics
- [x] Error tracking
- [x] Token generation metrics
- [x] Prompt token metrics
- [x] Generation speed metrics

### Phase 3 - Visualization

- [x] Grafana dashboards
- [x] Performance analytics
- [x] Model comparison dashboards

### Phase 4 - Logging

- [x] Loki integration
- [x] Centralized logs

### Phase 5 - Distributed Tracing

- [x] OpenTelemetry
- [x] Tempo integration
- [x] End-to-end request tracing

### Phase 6 - Cloud Native

- [x] Docker support
- [ ] Kubernetes deployment
- [ ] Helm charts
- [ ] Horizontal scaling

### Phase 7 - AI Operations

- [x] Multi-model request support
- [ ] Cost estimation
- [ ] Token analytics by tenant or application
- [ ] Model health monitoring
- [ ] AI workload observability dashboards

---

## Next Logical Additions

- add Kubernetes manifests or Helm
- add more tests for readiness, invalid requests and metrics output
