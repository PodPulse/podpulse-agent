# PodPulse Agent — Architecture

> **Scope:** W1–W6
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
│            │ Pattern match   │                         │
│            │ OOMKilled       │                         │
│            └────────┬────────┘                         │
│                     │                                  │
│                     ▼                                  │
│            ┌─────────────────┐                         │
│            │ ContextBuilder  │                         │
│            │                 │                         │
│            │ Assemble context│                         │
│            │ Owner chain     │                         │
│            │ Log tail        │                         │
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
| `IncidentDetector` | OOMKilled detection — dual-path (event stream + pod status) | ✅ W2 |
| `ContextBuilder` | Assemble incident context, owner chain, log tail, enforce payload size cap, strip secrets | ✅ W4 |
| `ReportEmitter` | Send `IncidentReport` + `x-api-key` metadata via gRPC, retry with exponential backoff | ✅ W5 |

---

## 3. Deployment (W6)

The agent is distributed as a Docker image and deployed via Helm.

**Docker image:** `ghcr.io/podpulse/podpulse-agent`
**Helm chart:** `https://github.com/PodPulse/podpulse-helm`

**Image build:**
- Base: `golang:1.25-alpine` (build) → `gcr.io/distroless/static-debian12` (runtime)
- Final image: ~5MB, no shell, no package manager
- Runs as `nonroot` (UID 65532)
- Build flags: `CGO_ENABLED=0 -trimpath -ldflags="-w -s"`

**Helm install:**
```bash
helm repo add podpulse https://podpulse.github.io/podpulse-helm
helm install podpulse-agent podpulse/podpulse-agent \
  --namespace podpulse \
  --create-namespace \
  --set agent.backend.address=api.podpulse.io:443 \
  --set agent.backend.apiKey=pk_live_...
```

**What the chart deploys:**
- `Deployment` — the agent pod (1 replica)
- `ServiceAccount` — K8s identity
- `ClusterRole` — read-only permissions
- `ClusterRoleBinding` — binds ServiceAccount to ClusterRole
- `Secret` — holds `PODPULSE_API_KEY` and `PODPULSE_BACKEND_ADDR`

**Automatic pod restart on secret change** via `checksum/secret` annotation on `spec.template`.

---

## 4. gRPC Contract (Agent → Backend)

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

**gRPC metadata (W5):**
```
x-api-key: pk_live_Abc123...  // per-cluster API key — set via PODPULSE_API_KEY env var
```

> The `raw_context` field contains a structured JSON payload built by `ContextBuilder`.
> Fields: `container`, `restart_count`, `memory_limit`, `memory_used_at_kill`,
> `owner_kind`, `owner_name`, `log_tail`.
> It never includes Kubernetes secrets or environment variable values.

---

## 5. RBAC — Required Permissions

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

## 6. Incident Detection — Scope

Detection is intentionally narrow for the MVP.
The goal is precision over recall: handle a small number of incident types well.

| Incident Type | Status |
|---|---|
| `OOMKilled` | ✅ W1–W4 |
| `CrashLoopBackOff` | 🔜 W8+ |
| `ImagePullBackOff` | 🔜 W8+ |
| `FailedScheduling` | 🔜 Post-MVP |
| `Pending (resource pressure)` | 🔜 Post-MVP |

---

## 7. Security Principles

- **Read-only** — the agent holds no write permissions, ever
- **No secrets** — Kubernetes secrets and environment variable values are never collected or transmitted
- **Bounded payload** — incident context is size-capped before transmission; the backend enforces the corresponding AI inference budget
- **No direct AI calls** — the agent never calls Claude or any external AI API
- **API key auth** — every gRPC call carries a per-cluster API key in metadata (`x-api-key`); key stored in a Kubernetes Secret
- **Minimal image** — distroless runtime, no shell, runs as nonroot
- **Optional anonymization** — pod names and namespaces can be anonymized before transmission (future)

---

## 8. Architectural Decision Records

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
**Decision:** OOMKilled is the first incident type. Additional types are added iteratively based on design partner feedback.
**Rationale:** A small number of well-handled incident types produces better diagnostics and higher SRE trust than broad but shallow coverage.

### ADR-006 — Dual-path OOMKilled detection
**Decision:** OOMKilled is detected via two complementary paths: event stream (`OOMKilling` reason) and pod status (`containerStatus.lastTerminationState.reason = OOMKilled`).
**Rationale:** Observed empirically on k3s — the `OOMKilling` event is not emitted by all runtimes. Relying on events alone produces false negatives. The pod status path is always populated regardless of runtime. Both paths are active simultaneously; the event path is kept for runtimes that do emit it. Re-triggering is prevented by comparing `restartCount` between old and new pod state.
**Known limitation:** Tested on k3s (containerd) only. Compatibility with CRI-O to be validated before first design partner onboarding.

### ADR-007 — ReportEmitter retry with exponential backoff
**Decision:** `ReportEmitter` retries failed gRPC calls up to 3 times with exponential backoff (500ms base delay). After all retries are exhausted, the error is logged and the agent continues — it never crashes.
**Rationale:** The backend may be temporarily unavailable (restart, deploy, network blip). The agent must remain operational regardless. Incidents are best-effort at this stage — deduplication and guaranteed delivery come with Redis in W5+.

### ADR-008 — Per-cluster API key authentication (W5)
**Decision:** The agent sends a per-cluster API key in gRPC metadata (`x-api-key`) on every call. The key is stored in a Kubernetes Secret and injected via Helm values.
**Rationale:** The backend is multi-tenant — it must identify which cluster is sending each incident report. API key per cluster gives fine-grained revocation (one compromised cluster does not affect others) and maps cleanly to Kubernetes Secrets for secure storage in-cluster.

### ADR-009 — Distroless runtime image (W6)
**Decision:** The agent runs on `gcr.io/distroless/static-debian12` with no shell and no package manager.
**Rationale:** Minimal attack surface for an in-cluster component with broad read permissions. No shell means no command injection even if the container is compromised. Consistent with enterprise security requirements (Elia).

### ADR-010 — Helm for deployment (W6)
**Decision:** The agent is distributed and deployed via Helm chart from a dedicated `podpulse-helm` repository.
**Rationale:** Helm is the de facto standard for Kubernetes application packaging. A dedicated chart repo allows independent versioning of the chart and the agent binary, and enables `helm repo add` for easy installation by design partners.

---

## 9. Out of Scope — current

- UI / dashboard
- Multi-cluster support per tenant
- Prometheus metrics / Grafana integration
- Incident types beyond OOMKilled (W8+)
- Payload anonymization
- CI/CD for image build (W8+)