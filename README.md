# LLM Observatory

<p align="center">
  <!-- Observatory image goes here -->
  <img src="./assets/img/observatory.jpg" width="500">
</p>

<p align="center">
  <strong>Observability platform for Large Language Models.</strong>
</p>

---

## Overview

LLM Observatory is an open-source project designed to monitor, analyze and understand Large Language Model workloads.

The project starts as a lightweight Go API connected to Ollama and will gradually evolve into a complete observability stack for AI applications.

The goal is not to build another chatbot, but to provide visibility into how LLMs behave in production environments.

---

## Current Features

### Chat API

Forward prompts to a local Ollama instance.

```http
POST /chat
```

Example request:

```json
{
  "prompt": "What is Kubernetes?"
}
```

Example response:

```json
{
  "prompt": "What is Kubernetes?",
  "answer": "Kubernetes is..."
}
```

### Ollama Integration


The API forwards requests to Ollama and returns the generated response.

### Lightweight Architecture

```text
Client
  ↓
Gin API
  ↓
Ollama
  ↓
Qwen
```

---

## Tech Stack

* Go
* Gin
* Ollama
* REST API

---

## Getting Started

### Requirements

* Go 1.22+
* Ollama

### Start Ollama

```bash
ollama run 'your model'
```

### Start the API

```bash
go run .
```

The server will be available at:

```text
http://localhost:8080
```

---

## Project Vision

Most AI projects focus on building applications.

LLM Observatory focuses on understanding, monitoring and operating LLM workloads.

The long-term objective is to provide a complete observability platform for AI systems.

---

## Roadmap

### Phase 1 — Core API

* [x] Go API
* [x] Ollama integration
* [x] Local inference
* [ ] Structured logging

### Phase 2 — Metrics

* [ ] Prometheus integration
* [ ] Request counter
* [ ] Latency metrics
* [ ] Error tracking
* [ ] Token generation metrics

### Phase 3 — Visualization

* [ ] Grafana dashboards
* [ ] Performance analytics
* [ ] Model comparison dashboards

### Phase 4 — Logging

* [ ] Loki integration
* [ ] Centralized logs
* [ ] Request tracing identifiers

### Phase 5 — Distributed Tracing

* [ ] OpenTelemetry
* [ ] Tempo integration
* [ ] End-to-end request tracing

### Phase 6 — Cloud Native

* [ ] Docker support
* [ ] Kubernetes deployment
* [ ] Helm charts
* [ ] Horizontal scaling

### Phase 7 — AI Operations

* [ ] Multi-model support
* [ ] Cost estimation
* [ ] Token analytics
* [ ] Model health monitoring
* [ ] AI workload observability dashboards

---

## Future Architecture

```text
Client
    ↓
API Gateway
    ↓
LLM Observatory
    ↓
Ollama / vLLM
    ↓
Models

Metrics ─────► Prometheus
Logs ────────► Loki
Traces ──────► Tempo

               ↓
            Grafana
```

---

## This README got translated by AI for a better understanding of the majority.
