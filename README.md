# Agent On Call

An autonomous incident response agent written in Go. When an alert fires via Alertmanager or PagerDuty, the agent investigates — querying Prometheus, Loki, deployment history, Kubernetes, and a runbook store, then posts a structured root-cause report to Slack. Typical time from alert to report: under 90 seconds.

## How it works

The agent runs a ReAct loop (Reason → Act → Observe) for each alert. An LLM receives the alert details and decides which tools to call and in what order. Each tool result is appended to the conversation and the model reasons over all accumulated evidence before producing a final structured answer.

Tools available to the LLM:
- `query_prometheus` — error rate, p99 latency, CPU, pod restarts
- `get_recent_deployments` — deploys in the last 2 hours, flagged if within 30 min of alert
- `query_loki` — top error patterns by frequency with a log sample
- `get_pod_status` — running/crashed pods, OOMKill, CrashLoopBackOff
- `search_runbooks` — full-text search over past incident runbooks

The output is a Slack Block Kit message with a confidence badge (High / Medium / Low), root cause, supporting evidence, and remediation steps. A threaded follow-up carries the raw data. Two feedback buttons let on-call engineers confirm or reject the finding — confirmed root causes are written back into the runbook store so future investigations benefit from them.

## Stack

| Component | Technology |

| Language | Go 1.22 |
| LLM | Groq API — `moonshotai/kimi-k2-instruct` |
| Webhook sources | Alertmanager, PagerDuty V3 |
| Metrics | Prometheus HTTP API |
| Logs | Loki (LogQL) |
| Deployments | PostgreSQL |
| Kubernetes | client-go |
| Runbook search | PostgreSQL full-text search (tsvector + GIN index) |
| Notifications | Slack Block Kit |
| Agent metrics | Prometheus + Grafana |

## Quick start

**Prerequisites:** Go 1.22+, Docker, Docker Compose, a [Groq API key](https://console.groq.com), a Slack bot token.

```bash
git clone https://github.com/flames31/oncall-agent
cd oncall-agent
cp .env.example .env
# Edit .env — add GROQ_API_KEY and SLACK_BOT_TOKEN

cd deploy && docker compose up -d --build

# Seed runbooks (once)
cd .. && go run scripts/seed_runbooks.go

# Trigger the demo
curl http://localhost:9001/break
# Report arrives in #incidents within ~90 seconds

curl http://localhost:9001/fix
```

Grafana dashboard: http://localhost:3000

---

## Configuration

Secrets are loaded from `.env` via `${VAR}` expansion in `config.yaml`. Notable settings:

| Key | Default | Description |

| `groq_model` | `moonshotai/kimi-k2-instruct` | Any Groq model with tool-use support |
| `worker_count` | `5` | Parallel investigation workers |
| `dedup_window_seconds` | `30` | Drops duplicate alerts within this window |
| `max_llm_iterations` | `5` | Max tool calls per investigation |
| `investigation_timeout_seconds` | `45` | Hard deadline per investigation |

## Endpoints

| Endpoint | Description |

| `POST /webhook` | Alertmanager or PagerDuty V3 webhook |
| `POST /slack/actions` | Slack button click payloads |
| `GET /healthz` | Health check — returns `ok` |
| `GET /metrics` | Prometheus metrics for the agent itself |

## Project layout

```
cmd/agent/          entry point
cmd/demo-service/   breakable HTTP service used in the demo
internal/
  config/           YAML config loader
  webhook/          Alertmanager and PagerDuty parsers
  investigation/    worker pool, orchestrator, deduplicator, Prometheus metrics
  tools/            one file per data source (Prometheus, Loki, k8s, deployments, runbooks)
  llm/              Groq client, tool definitions, ReAct loop, dispatcher
  report/           Slack Block Kit builder and feedback parser
  store/            PostgreSQL migrations, feedback writes, runbook upserts
deploy/             Docker Compose stack, Prometheus rules, Grafana dashboard
scripts/            runbook seeding script
```
