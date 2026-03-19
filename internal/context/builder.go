package context

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	appsv1lister "k8s.io/client-go/listers/apps/v1"
)

type IncidentContext struct {
	IncidentType     string
	Namespace        string
	PodName          string
	NodeName         string
	ContainerName    string
	RestartCount     int32
	MemoryLimit      string
	MemoryUsedAtKill string
	RawEvents        []string
	OwnerKind        string // e.g. "Deployment", "StatefulSet", "DaemonSet", "Job", "" (standalone pod)
	OwnerName        string // name of the final owner (not the intermediate ReplicaSet)
}

type ContextBuilder interface {
	Build(pod *corev1.Pod, event *corev1.Event, rsLister appsv1lister.ReplicaSetLister) (*IncidentContext, error)
}

type OOMContextBuilder struct{}

func NewOOMContextBuilder() *OOMContextBuilder {
	return &OOMContextBuilder{}
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

	for i, cs := range pod.Status.ContainerStatuses {
		if cs.LastTerminationState.Terminated == nil ||
			cs.LastTerminationState.Terminated.Reason != "OOMKilled" {
			continue
		}

		ctx.ContainerName = cs.Name
		ctx.RestartCount = cs.RestartCount

		if i < len(pod.Spec.Containers) {
			if limit, ok := pod.Spec.Containers[i].Resources.Limits[corev1.ResourceMemory]; ok {
				ctx.MemoryLimit = limit.String()
			}
		}

		if pod.Status.ContainerStatuses[i].LastTerminationState.Terminated.Message != "" {
			ctx.MemoryUsedAtKill = pod.Status.ContainerStatuses[i].LastTerminationState.Terminated.Message
		}

		break
	}

	resolveOwner(ctx, pod, rsLister)

	return ctx, nil
}

// resolveOwner walks pod.OwnerReferences to determine the final controlling workload.
// If the direct owner is a ReplicaSet, it looks up the ReplicaSet in the cache to find
// the owning Deployment. All resolution is cache-only — no API calls are made.
func resolveOwner(ctx *IncidentContext, pod *corev1.Pod, rsLister appsv1lister.ReplicaSetLister) {
	if len(pod.OwnerReferences) == 0 {
		// Standalone pod — leave OwnerKind/OwnerName empty
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
