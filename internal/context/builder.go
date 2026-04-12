package context

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	appsv1lister "k8s.io/client-go/listers/apps/v1"

	"github.com/PodPulse/podpulse-agent/internal/collector"
)

// IncidentContext holds all data collected by a context builder for one incident.
type IncidentContext struct {
	IncidentType     string
	Namespace        string
	PodName          string
	NodeName         string
	ContainerName    string
	RestartCount     int32
	MemoryLimit      string
	MemoryUsedAtKill string
	LastExitCode     int32
	LastExitReason   string
	RawEvents        []string
	OwnerKind        string
	OwnerName        string
	LogTail          string
	// Resource fields (OOM only)
	MemoryRequest string
	CPULimit      string
	CPURequest    string
	// Previous-container logs (both OOM and CrashLoop)
	PreviousLogTail string
	// Enrichment context (both OOM and CrashLoop)
	ManifestCtx ManifestContext
	DeployCtx   DeployContext
}

// ContextBuilder builds an IncidentContext for a detected pod incident.
type ContextBuilder interface {
	Build(pod *corev1.Pod, event *corev1.Event, rsLister appsv1lister.ReplicaSetLister) (*IncidentContext, error)
}

// --- OOMKilled ---

type OOMContextBuilder struct {
	logCollector    *collector.LogCollector
	manifestBuilder *ManifestContextBuilder
	deployBuilder   *DeployContextBuilder
}

func NewOOMContextBuilder(
	lc *collector.LogCollector,
	mb *ManifestContextBuilder,
	db *DeployContextBuilder,
) *OOMContextBuilder {
	return &OOMContextBuilder{
		logCollector:    lc,
		manifestBuilder: mb,
		deployBuilder:   db,
	}
}

func (b *OOMContextBuilder) Build(pod *corev1.Pod, event *corev1.Event, rsLister appsv1lister.ReplicaSetLister) (*IncidentContext, error) {
	ctx := &IncidentContext{
		IncidentType: "OOMKilled",
		Namespace:    pod.Namespace,
		PodName:      pod.Name,
		NodeName:     pod.Spec.NodeName,
		RawEvents:    []string{},
	}

	if event != nil {
		ctx.RawEvents = append(ctx.RawEvents, event.Message)
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.LastTerminationState.Terminated == nil ||
			cs.LastTerminationState.Terminated.Reason != "OOMKilled" {
			continue
		}

		ctx.ContainerName = cs.Name
		ctx.RestartCount = cs.RestartCount

		// Match container spec by name — index order is not guaranteed.
		if spec := findContainerSpec(pod, cs.Name); spec != nil {
			if limit, ok := spec.Resources.Limits[corev1.ResourceMemory]; ok {
				ctx.MemoryLimit = limit.String()
			}
			if req, ok := spec.Resources.Requests[corev1.ResourceMemory]; ok {
				ctx.MemoryRequest = req.String()
			}
			if limit, ok := spec.Resources.Limits[corev1.ResourceCPU]; ok {
				ctx.CPULimit = limit.String()
			}
			if req, ok := spec.Resources.Requests[corev1.ResourceCPU]; ok {
				ctx.CPURequest = req.String()
			}
		}

		if cs.LastTerminationState.Terminated.Message != "" {
			ctx.MemoryUsedAtKill = cs.LastTerminationState.Terminated.Message
		}

		break
	}

	resolveOwner(ctx, pod, rsLister)

	if ctx.ContainerName != "" && b.logCollector != nil {
		logCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ctx.LogTail = b.logCollector.Collect(logCtx, ctx.Namespace, ctx.PodName, ctx.ContainerName)
		ctx.PreviousLogTail = b.logCollector.CollectPrevious(logCtx, ctx.Namespace, ctx.PodName, ctx.ContainerName)
	}

	manifestCtx, manifestCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer manifestCancel()
	ctx.ManifestCtx = b.manifestBuilder.Build(manifestCtx, pod)
	ctx.DeployCtx = b.deployBuilder.Build(pod, deploymentName(ctx))

	return ctx, nil
}

// --- CrashLoopBackOff ---

type CrashLoopContextBuilder struct {
	logCollector    *collector.LogCollector
	manifestBuilder *ManifestContextBuilder
	deployBuilder   *DeployContextBuilder
}

func NewCrashLoopContextBuilder(
	lc *collector.LogCollector,
	mb *ManifestContextBuilder,
	db *DeployContextBuilder,
) *CrashLoopContextBuilder {
	return &CrashLoopContextBuilder{
		logCollector:    lc,
		manifestBuilder: mb,
		deployBuilder:   db,
	}
}

func (b *CrashLoopContextBuilder) Build(pod *corev1.Pod, event *corev1.Event, rsLister appsv1lister.ReplicaSetLister) (*IncidentContext, error) {
	ctx := &IncidentContext{
		IncidentType: "CrashLoopBackOff",
		Namespace:    pod.Namespace,
		PodName:      pod.Name,
		NodeName:     pod.Spec.NodeName,
		RawEvents:    []string{},
	}

	if event != nil {
		ctx.RawEvents = append(ctx.RawEvents, event.Message)
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.RestartCount < 3 {
			continue
		}
		if cs.LastTerminationState.Terminated == nil {
			continue
		}
		if cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
			continue
		}

		ctx.ContainerName = cs.Name
		ctx.RestartCount = cs.RestartCount
		ctx.LastExitCode = cs.LastTerminationState.Terminated.ExitCode
		ctx.LastExitReason = cs.LastTerminationState.Terminated.Reason

		break
	}

	resolveOwner(ctx, pod, rsLister)

	if ctx.ContainerName != "" && b.logCollector != nil {
		logCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ctx.LogTail = b.logCollector.Collect(logCtx, ctx.Namespace, ctx.PodName, ctx.ContainerName)
		ctx.PreviousLogTail = b.logCollector.CollectPrevious(logCtx, ctx.Namespace, ctx.PodName, ctx.ContainerName)
	}

	manifestCtx2, manifestCancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer manifestCancel2()
	ctx.ManifestCtx = b.manifestBuilder.Build(manifestCtx2, pod)
	ctx.DeployCtx = b.deployBuilder.Build(pod, deploymentName(ctx))

	return ctx, nil
}

// --- helpers ---

// findContainerSpec returns the Spec.Container matching containerName, or nil.
func findContainerSpec(pod *corev1.Pod, containerName string) *corev1.Container {
	for i := range pod.Spec.Containers {
		if pod.Spec.Containers[i].Name == containerName {
			return &pod.Spec.Containers[i]
		}
	}
	return nil
}

// deploymentName returns ctx.OwnerName when the owner is a Deployment, else "".
func deploymentName(ctx *IncidentContext) string {
	if ctx.OwnerKind == "Deployment" {
		return ctx.OwnerName
	}
	return ""
}

// resolveOwner walks OwnerReferences to find the top-level workload owner.
func resolveOwner(ctx *IncidentContext, pod *corev1.Pod, rsLister appsv1lister.ReplicaSetLister) {
	if len(pod.OwnerReferences) == 0 {
		return
	}

	ref := pod.OwnerReferences[0]

	switch ref.Kind {
	case "ReplicaSet":
		rs, err := rsLister.ReplicaSets(pod.Namespace).Get(ref.Name)
		if err != nil {
			fmt.Printf("[WARN] could not resolve ReplicaSet %s/%s from cache: %v\n",
				pod.Namespace, ref.Name, err)
			return
		}
		if len(rs.OwnerReferences) == 0 {
			fmt.Printf("[WARN] ReplicaSet %s/%s has no owner — skipping owner resolution\n",
				pod.Namespace, ref.Name)
			return
		}
		rsOwner := rs.OwnerReferences[0]
		if rsOwner.Kind != "Deployment" {
			fmt.Printf("[WARN] unexpected owner kind %q for ReplicaSet %s/%s — skipping\n",
				rsOwner.Kind, pod.Namespace, ref.Name)
			return
		}
		ctx.OwnerKind = "Deployment"
		ctx.OwnerName = rsOwner.Name

	case "Deployment":
		ctx.OwnerKind = "Deployment"
		ctx.OwnerName = ref.Name

	case "StatefulSet":
		ctx.OwnerKind = "StatefulSet"
		ctx.OwnerName = ref.Name

	case "DaemonSet":
		ctx.OwnerKind = "DaemonSet"
		ctx.OwnerName = ref.Name

	case "Job":
		ctx.OwnerKind = "Job"
		ctx.OwnerName = ref.Name

	default:
		fmt.Printf("[WARN] unrecognized owner kind %q for pod %s/%s — leaving owner fields empty\n",
			ref.Kind, pod.Namespace, pod.Name)
	}
}
