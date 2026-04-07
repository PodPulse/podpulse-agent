# PodPulse Agent — Architecture

> **Scope:** W1–W8
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
│   │  - Emit IncidentReport via gRPC + x-api-key  │   │
│   └──────────────────────┬───────────────────────┘   │
└──────────────────────────────────────────────────────┘
                           │
                           │  gRPC (IncidentReport + x-api-key metadata)
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
│            │ OOMKilled       │                         │
│            │ CrashLoopBackOff│                         │
│            └────────┬────────┘                         │
│                     │                                  │
│                     ▼                                  │
│            ┌─────────────────┐                         │
│            │ ContextBuilder  │                         │
│            │ (per type)      │                         │
│            │                 │                         │
│            │ OOMContextBuilder         │               │
│            │ CrashLoopContextBuilder   │               │
│            │ Owner chain resolution    │               │
│            │ Log tail (previous ctr)   │               │
│            │ Bounded payload           │               │
│            │ No secrets                │               │
│            └────────┬────────┘                         │
│                     │                                  │
│                     ▼                                  │
│            ┌─────────────────┐                         │
│            │  ReportEmitter  │                         │
│            │                 │                         │
│            │ Send Incident   │                         │
│            │ Report via gRPC │                         │
│            │ + x-api-key     │                         │
│            └─────────────────┘                         │
└─────────────────────────────────────────────────────────┘
```

### Component responsibilities

| Component | Responsibility | Status |
|---|---|---|
| `EventWatcher` | Watch Kubernetes events via `client-go` informers | ✅ W1–W2 |
| `PodWatcher` | Watch pod lifecycle and container status changes | ✅ W1–W2 |
| `LogCollector` | Fetch container logs on incident trigger (bounded tail, previous container) | ✅ W4 |
| `IncidentDetector` | OOMKilled + CrashLoopBackOff detection | ✅ W8 |
| `OOMContextBuilder` | Assemble OOMKilled context — memory limit, owner chain, log tail | ✅ W4 |
| `CrashLoopContextBuilder` | Assemble CrashLoopBackOff context — exit code, last reason, owner chain, log tail | ✅ W8 |
| `ReportEmitter` | Send `IncidentReport` + `x-api-key` via gRPC, retry with exponential backoff | ✅ W5 |

---

## 3. Deployment (W6+)

The agent is distributed as a Docker image and deployed via Helm.

**Docker image:** `ghcr.io/podpulse/podpulse-agent`
**Helm chart:** `https://podpulse.github.io/podpulse-helm`

**Image build:**
- Base: `golang:1.23-alpine` (build) → `gcr.io/distroless/static-debian12` (runtime)
- Final image: ~5MB, no shell, no package manager
- Runs as `nonroot` (UID 65532)
- Build flags: `CGO_ENABLED=0 -trimpath -ldflags="-w -s"`

**Helm install:**
```bash
helm repo add podpulse https://podpulse.github.io/podpulse-helm
helm install podpulse-agent podpulse/podpulse-agent \
  --namespace podpulse \
  --create-namespace \
  --set agent.backend.address=api.podpulse.io:5051 \
  --set agent.backend.apiKey=pk_live_...
```

**What the chart deploys:**
- `Deployment` — the agent pod (1 replica, `imagePullPolicy: Always`)
- `ServiceAccount` — K8s identity
- `ClusterRole` — read-only permissions
- `ClusterRoleBinding` — binds ServiceAccount to ClusterRole
- `Secret` — holds `PODPULSE_API_KEY` and `PODPULSE_BACKEND_ADDR`

**Automatic pod restart on secret change** via `checksum/secret` annotation on `spec.template`.

**CI/CD (W8):**
- `build.yml` — triggered on every push, runs `go build` + `go test`
- `release.yml` — triggered on tag `v*`, builds and pushes `ghcr.io/podpulse/podpulse-agent:{tag}` + `:latest`

---

## 4. gRPC Contract (Agent → Backend)

```protobuf
syntax = "proto3";

package podpulse.v1;

service IncidentService {
  rpc ReportIncident (IncidentReport) returns (ReportAck);
}

message IncidentReport {
  string incident_id    = 1;
  string incident_type  = 2; // OOMKilled | CrashLoopBackOff
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

**gRPC metadata:**
```
x-api-key: pk_live_...  // per-cluster API key
```

**`raw_context` fields by incident type:**

OOMKilled:
```json
{
  "container": "api",
  "restart_count": 4,
  "memory_limit": "256Mi",
  "memory_used_at_kill": "",
  "owner_kind": "Deployment",
  "owner_name": "payment-api",
  "log_tail": "..."
}
```

CrashLoopBackOff:
```json
{
  "container": "api",
  "restart_count": 5,
  "last_exit_code": 1,
  "last_exit_reason": "Error",
  "owner_kind": "Deployment",
  "owner_name": "payment-api",
  "log_tail": "..."
}
```

> Kubernetes Secrets and environment variable values are never included in `raw_context`.

---

## 5. RBAC — Required Permissions

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

## 6. Incident Detection — W8 Scope

| Incident Type | Detection Logic | Status |
|---|---|---|
| `OOMKilled` | `lastTerminationState.reason = OOMKilled` + restart count delta | ✅ W4 |
| `CrashLoopBackOff` | `restartCount >= 3` + `lastExitReason != OOMKilled` + restart count delta | ✅ W8 |
| `ImagePullBackOff` | — | 🔜 Post-MVP |
| `FailedScheduling` | — | 🔜 Post-MVP |
| `Pending (resource pressure)` | — | 🔜 Post-MVP |

**CrashLoopBackOff trigger policy:**

The agent does not rely on `State.Waiting.Reason = CrashLoopBackOff` alone — this state is ephemeral and may not be present at the exact moment the informer fires. Instead:

1. `restartCount >= 3`
2. `lastTerminationState.Terminated != nil`
3. `lastTerminationState.Terminated.Reason != OOMKilled` (OOMKilled is handled separately)
4. `restartCount` changed between old and new pod state (dedup guard)

**Anti-burst local guard:** A `ConcurrentMap` with a 10-minute TTL prevents the agent from emitting multiple reports for the same pod during a rapid crash cycle. This is not the deduplication source of truth — that lives in the backend (Redis + DB).

---

## 7. Configuration

| Flag | Env var | Default | Description |
|---|---|---|---|
| `--backend-addr` | `PODPULSE_BACKEND_ADDR` | `localhost:5051` | gRPC backend address |
| `--api-key` | `PODPULSE_API_KEY` | — | Per-cluster API key |
| `--debug` | `PODPULSE_DEBUG=true` | `false` | Enable debug logging (container state on every pod update) |

---

## 8. Security Principles

- **Read-only** — the agent holds no write permissions, ever
- **No secrets** — Kubernetes secrets and environment variable values are never collected or transmitted
- **Bounded payload** — incident context is size-capped before transmission
- **No direct AI calls** — the agent never calls Claude or any external AI API
- **API key auth** — every gRPC call carries a per-cluster API key in metadata
- **Minimal image** — distroless runtime, no shell, runs as nonroot
- **Optional anonymization** — pod names and namespaces can be anonymized before transmission (future)

---

## 9. Architectural Decision Records

### ADR-001 — Go for the in-cluster agent
**Decision:** The agent is written in Go.
**Rationale:** `client-go` is the Kubernetes-native client library. Static binary, low memory footprint, straightforward Helm deployment. No alternative is justifiable for an in-cluster agent.

### ADR-002 — gRPC for Agent → Backend communication
**Decision:** gRPC with a Protobuf contract.
**Rationale:** Strongly typed contract, efficient binary serialization, natural fit for structured streaming. HTTP/REST would add unnecessary overhead with no benefit for this internal channel.

### ADR-003 — Bounded payload size
**Decision:** `ContextBuilder` enforces a hard payload size cap on the incident context before transmission.
**Rationale:** The agent has no knowledge of AI inference budgets — that is a backend concern. The cap is expressed in bytes at the agent level. The backend is responsible for mapping that to its own token limit.

### ADR-004 — No direct AI calls from the agent
**Decision:** The agent never calls Claude or any external AI API.
**Rationale:** Separation of concerns. The agent observes and assembles context — it does not analyze. This also keeps the agent's network policy simple: egress to the backend endpoint only.

### ADR-005 — Precision over recall for incident detection
**Decision:** W8 covers OOMKilled and CrashLoopBackOff. Additional types are added iteratively based on design partner feedback.
**Rationale:** A small number of well-handled incident types produces better diagnostics and higher SRE trust than broad but shallow coverage.

### ADR-006 — Dual-path OOMKilled detection
**Decision:** OOMKilled is detected via two complementary paths: event stream (`OOMKilling` reason) and pod status (`lastTerminationState.reason = OOMKilled`).
**Rationale:** Observed empirically on k3s — the `OOMKilling` event is not emitted by all runtimes. Both paths are active simultaneously.

### ADR-007 — ReportEmitter retry with exponential backoff
**Decision:** `ReportEmitter` retries failed gRPC calls up to 3 times with exponential backoff (500ms base delay). After all retries are exhausted, the error is logged and the agent continues.
**Rationale:** The backend may be temporarily unavailable. The agent must remain operational regardless.

### ADR-008 — Per-cluster API key authentication
**Decision:** The agent sends a per-cluster API key in gRPC metadata (`x-api-key`) on every call.
**Rationale:** The backend is multi-tenant. API key per cluster gives fine-grained revocation.

### ADR-009 — Distroless runtime image
**Decision:** The agent runs on `gcr.io/distroless/static-debian12` with no shell and no package manager.
**Rationale:** Minimal attack surface for an in-cluster component with broad read permissions.

### ADR-010 — Helm for deployment
**Decision:** The agent is distributed and deployed via Helm chart from a dedicated `podpulse-helm` repository.
**Rationale:** Helm is the de facto standard for Kubernetes application packaging. Enables `helm repo add` for easy installation by design partners.

### ADR-011 — Per-type ContextBuilder (W8)
**Decision:** Each incident type has its own `ContextBuilder` implementation (`OOMContextBuilder`, `CrashLoopContextBuilder`). The `IncidentDetector` selects the appropriate builder and passes it to `buildAndEmit`.
**Rationale:** OOMKilled and CrashLoopBackOff require different fields — memory limit vs exit code, different container status fields. A single generic builder would require nullable fields and conditional logic. Separate builders keep each type self-contained and independently testable.

### ADR-012 — CrashLoopBackOff trigger on restartCount, not Waiting.Reason (W8)
**Decision:** CrashLoopBackOff is triggered on `restartCount >= 3` + `lastExitReason != OOMKilled`, not on `State.Waiting.Reason = CrashLoopBackOff`.
**Rationale:** `State.Waiting.Reason` is ephemeral — the informer may fire when the container is in `Running` or `Terminated` state. `lastTerminationState` is stable and always populated after a crash. Empirically validated on k3d.

---

## 10. Out of Scope — current

- UI / dashboard
- Multi-cluster support per tenant
- Prometheus metrics / Grafana integration
- Incident types beyond OOMKilled and CrashLoopBackOff
- Payload anonymization