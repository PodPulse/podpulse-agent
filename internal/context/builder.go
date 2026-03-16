package context

import (
    corev1 "k8s.io/api/core/v1"
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
}

type ContextBuilder interface {
    Build(pod *corev1.Pod, event *corev1.Event) (*IncidentContext, error)
}

type OOMContextBuilder struct{}

func NewOOMContextBuilder() *OOMContextBuilder {
    return &OOMContextBuilder{}
}

func (b *OOMContextBuilder) Build(pod *corev1.Pod, event *corev1.Event) (*IncidentContext, error) {
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
        ctx.RestartCount  = cs.RestartCount

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

    return ctx, nil
}