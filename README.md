# PodPulse Agent

> ⚠️ **Early development** — We are looking for design partners running Kubernetes in production. [Get in touch](#status)

**AI-powered Kubernetes incident diagnostics.**

PodPulse Agent monitors your Kubernetes cluster in real time, detects production incidents, analyzes root causes using AI (powered by Claude), and generates actionable diagnostics — and eventually automated fixes.

The goal is simple: turn Kubernetes incidents into clear explanations and solutions in seconds.

---

## Why PodPulse?

Operating Kubernetes in production often means spending hours investigating:

- OOMKilled pods
- CrashLoopBackOff
- Failed deployments
- Misconfigured resources
- Memory leaks
- Autoscaling issues

Engineers typically have to manually inspect `kubectl describe`, `kubectl logs`, `kubectl events`, metrics, and Git history.

**PodPulse automates that investigation.**

Instead of debugging manually, PodPulse analyzes incidents and produces a structured diagnostic.

### Example output

```
Incident detected: Pod OOMKilled

Namespace: payments
Pod:       payment-api-7c9d5d
Node:      aks-nodepool1-3821

Root cause:
  Container memory limit (256Mi) too low for workload.
  Recent commit increased batch processing size.

Evidence:
  - Pod restarted 4 times in 3 minutes
  - Container memory usage reached 248Mi
  - Limit set to 256Mi
  - Previous deployment used 512Mi

Suggested fix:
  Increase memory limit to 512Mi

Confidence score: 0.87
```

---

## Features (MVP)

**Current capabilities:**
- Kubernetes event streaming
- Detection of common pod failures
- Log collection
- Incident context building
- AI-powered root cause analysis (powered by Claude)

**Early detection patterns:**
- `OOMKilled`
- `CrashLoopBackOff`
- `ImagePullBackOff`
- `FailedScheduling`

**Future capabilities:**
- GitHub PR generation with fixes
- Resource optimization recommendations
- Multi-cluster monitoring
- Historical incident learning
- Confidence scoring

---

## Architecture

```
Kubernetes Cluster
       │
       │  Events / Logs / Pod State
       ▼
PodPulse Agent (in-cluster, Helm-deployed)
       │
       │  Structured incident context
       ▼
PodPulse Backend
       │
       ▼
AI Diagnostics Engine (powered by Claude)
       │
       ▼
Structured Diagnosis
       │
       ├── CLI output
       ├── Slack alert
       └── GitHub PR (future)
```

The agent runs inside your cluster and observes:
- Pod lifecycle events
- Container status
- Kubernetes events
- Logs

It sends structured incident data to the PodPulse diagnostic engine. The agent itself never calls any external AI API — all analysis happens in the backend.

---

## Installation

> **Helm chart coming soon.** Star this repo to be notified when the first release is available.

---

## Permissions

The agent requires read-only access to:
- Pods
- Events
- Nodes
- Namespaces
- Deployments

Example RBAC:

```yaml
apiGroups: [""]
resources:
  - pods
  - events
  - nodes
  - namespaces
verbs: ["get", "list", "watch"]
```

**The agent never modifies cluster resources.**

---

## Security

PodPulse is designed with security in mind:

- **Read-only** Kubernetes permissions
- **No cluster modifications** — ever
- **Sensitive data filtering** before any transmission
- **Optional anonymization** of pod names and namespaces
- **Secrets and environment variables are never transmitted**

---

## Roadmap

**Phase 1 — Agent** *(current)*
- [ ] Event streaming
- [ ] Incident detection
- [ ] Basic diagnostics

**Phase 2 — Automated investigation**
- [ ] Git commit correlation
- [ ] Metrics integration
- [ ] Confidence scoring

**Phase 3 — Automated remediation**
- [ ] Pull requests with fixes
- [ ] Resource tuning recommendations
- [ ] Self-healing suggestions

---

## Status

PodPulse is currently in early development.

We are looking for **design partners** — teams running Kubernetes in production who are interested in trying the agent and providing feedback.

If that's you, open a [Discussion](https://github.com/PodPulse/podpulse-agent/discussions) or reach out directly.

---

## Contributing

PodPulse is open-core. This repository contains the in-cluster agent component, which is open source under Apache 2.0.

If you are interested in Kubernetes observability, AI-assisted operations, or platform engineering — contributions and feedback are welcome.

Please open an issue or discussion before submitting a pull request.

---

## License

[Apache 2.0](LICENSE)

---

## Vision

The long-term vision of PodPulse is to become an **AI SRE teammate** that continuously monitors clusters, diagnoses incidents, and suggests fixes automatically — so your engineering team can focus on building product instead of fighting infrastructure.
