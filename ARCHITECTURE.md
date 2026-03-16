# PodPulse Agent — Architecture

> **Scope:** W1–W4 (MVP — OOMKilled end-to-end + GitHub PR generation)
> This document covers the in-cluster agent component hosted in this repository.
> The PodPulse backend is a separate, private service.

---

## 1. System Overview (C4 — Level 1)

```
┌──────────────────────────────────────────────────────┐
│                  Kubernetes Cluster                  │
│                                                      │
│   ┌──────────────────────────────────────────────┐   │
│   │           PodPulse Agent (Go)                │   │
│   │                                              │   │
│   │  - Watch pods, events, logs (read-only)      │   │
│   │  - Detect incident patterns                  │   │
│   │  - Build structured incident context         │   │
│   │  - Emit IncidentReport via gRPC              │   │
│   └──────────────────────┬───────────────────────┘   │
└──────────────────────────────────────────────────────┘
                           │
                           │  gRPC (IncidentReport)
                           ▼
            ┌──────────────────────────┐
            │   PodPulse Backend       │
            │   (private service)      │
            │                          │
            │  - AI diagnostic engine  │
            │  - GitHub PR generation  │
            └──────────────────────────┘
                           │
               ┌───────────┴───────────┐
               ▼                       ▼
          Claude API              GitHub API
          (Sonnet)                (PR only)
```

The agent is the only component that runs inside your cluster.
It never calls any external AI API directly — all analysis happens in the backend.

---

## 2. Agent — Internal Components (C4 — Level 2)

```
┌─────────────────────────────────────────────────────────┐
│                   PodPulse Agent                        │
│                                                         │
│  ┌─────────────┐   ┌─────────────┐   ┌───────────────┐ │
│  │ EventWatcher│   │  PodWatcher │   │ LogCollector  │ │
│  │             │   │             │   │               │ │
│  │ Watch K8s   │   │ Watch pod   │   │ Fetch logs on │ │
│  │ events via  │   │ lifecycle & │   │ incident      │ │
│  │ informers   │   │ container   │   │ trigger       │ │
│  │             │   │ status      │   │ (tail,bounded)│ │
│  └──────┬──────┘   └──────┬──────┘   └───────┬───────┘ │
│         └────────────┬────┘                  │         │
│                      ▼                       │         │
│            ┌─────────────────┐               │         │
│            │IncidentDetector │◄──────────────┘         │
│            │                 │                         │
│            │ Pattern match   │                         │
│            │ W1-W4: OOMKilled│                         │
│            └────────┬────────┘                         │
│                     │                                  │
│                     ▼                                  │
│            ┌─────────────────┐                         │
│            │ ContextBuilder  │                         │
│            │                 │                         │
│            │ Assemble context│                         │
│            │ Bounded payload │                         │
│            │ No secrets      │                         │
│            └────────┬────────┘                         │
│                     │                                  │
│                     ▼                                  │
│            ┌─────────────────┐                         │
│            │  ReportEmitter  │                         │
│            │                 │                         │
│            │ Send Incident   │                         │
│            │ Report via gRPC │                         │
│            └─────────────────┘                         │
└─────────────────────────────────────────────────────────┘
```

### Component responsibilities

| Component | Responsibility |
|---|---|
| `EventWatcher` | Watch Kubernetes events via `client-go` informers |
| `PodWatcher` | Watch pod lifecycle and container status changes |
| `LogCollector` | Fetch container logs on incident trigger (bounded tail) |
| `IncidentDetector` | Pattern matching against known incident types |
| `ContextBuilder` | Assemble structured incident context, enforce payload size cap, strip secrets |
| `ReportEmitter` | Send `IncidentReport` to the PodPulse backend via gRPC |

---

## 3. gRPC Contract (Agent → Backend)

The agent communicates with the backend through a single gRPC service.

```protobuf
syntax = "proto3";

package podpulse.v1;

service IncidentService {
  rpc ReportIncident (IncidentReport) returns (ReportAck);
}

message IncidentReport {
  string incident_id    = 1;
  string incident_type  = 2; // e.g. OOMKilled, CrashLoopBackOff
  string namespace      = 3;
  string pod_name       = 4;
  string node_name      = 5;
  int32  restart_count  = 6;
  string raw_context    = 7; // JSON-serialized, size-bounded
  int64  detected_at    = 8; // Unix timestamp (UTC)
}

message ReportAck {
  string incident_id = 1;
  bool   accepted    = 2;
}
```

> The `raw_context` field contains a structured JSON payload built by `ContextBuilder`.
> It never includes Kubernetes secrets or environment variable values.

---

## 4. RBAC — Required Permissions

The agent requires **read-only** access to the following resources:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: podpulse-agent
rules:
  - apiGroups: [""]
    resources:
      - pods
      - pods/log
      - events
      - nodes
      - namespaces
    verbs: ["get", "list", "watch"]
  - apiGroups: ["apps"]
    resources:
      - deployments
      - replicasets
    verbs: ["get", "list", "watch"]
```

**The agent never modifies any cluster resource.**

---

## 5. Incident Detection — W1–W4 Scope

Detection is intentionally narrow for the MVP.
The goal is precision over recall: handle a small number of incident types well.

| Incident Type | Status |
|---|---|
| `OOMKilled` | ✅ W1–W4 |
| `CrashLoopBackOff` | 🔜 W5–W6 |
| `ImagePullBackOff` | 🔜 W7–W8 |
| `FailedScheduling` | 🔜 Post-MVP |
| `Pending (resource pressure)` | 🔜 Post-MVP |

---

## 6. Security Principles

- **Read-only** — the agent holds no write permissions, ever
- **No secrets** — Kubernetes secrets and environment variable values are never collected or transmitted
- **Bounded payload** — incident context is size-capped before transmission; the backend enforces the corresponding AI inference budget
- **No direct AI calls** — the agent never calls Claude or any external AI API
- **Optional anonymization** — pod names and namespaces can be anonymized before transmission (future)

---

## 7. Architectural Decision Records

### ADR-001 — Go for the in-cluster agent
**Decision:** The agent is written in Go.
**Rationale:** `client-go` is the Kubernetes-native client library. Static binary, low memory footprint, straightforward Helm deployment. No alternative is justifiable for an in-cluster agent.

### ADR-002 — gRPC for Agent → Backend communication
**Decision:** gRPC with a Protobuf contract.
**Rationale:** Strongly typed contract, efficient binary serialization, natural fit for structured streaming. HTTP/REST would add unnecessary overhead with no benefit for this internal channel.

### ADR-003 — Bounded payload size
**Decision:** `ContextBuilder` enforces a hard payload size cap on the incident context before transmission.
**Rationale:** The agent has no knowledge of AI inference budgets — that is a backend concern. The cap is expressed in bytes at the agent level. The backend is responsible for mapping that to its own token limit. This keeps the abstraction boundary clean and prevents log dumps from inflating payloads regardless of what the backend does with them.

### ADR-004 — No direct AI calls from the agent
**Decision:** The agent never calls Claude or any external AI API.
**Rationale:** Separation of concerns. The agent observes and assembles context — it does not analyze. This also keeps the agent's network policy simple: egress to the backend endpoint only.

### ADR-005 — Precision over recall for incident detection
**Decision:** W1–W4 covers OOMKilled only. Additional incident types are added iteratively.
**Rationale:** A small number of well-handled incident types produces better diagnostics and higher SRE trust than broad but shallow coverage.

---

## 8. Out of Scope — W1–W4

The following are intentionally excluded from the current phase:

- Redis (deduplication, pattern caching) — introduced post W4
- UI / dashboard
- Multi-cluster support
- Prometheus metrics / Grafana integration
- Auth and API key management
- Incident history and persistence
- Incident types beyond OOMKilled
