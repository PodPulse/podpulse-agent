package detector

import (
    "fmt"

    corev1 "k8s.io/api/core/v1"
    appsv1lister "k8s.io/client-go/listers/apps/v1"
    v1lister "k8s.io/client-go/listers/core/v1"

    contextbuilder "github.com/PodPulse/podpulse-agent/internal/context"
    "github.com/PodPulse/podpulse-agent/internal/emitter"
)

type IncidentDetector struct {
    podLister      v1lister.PodLister
    rsLister       appsv1lister.ReplicaSetLister
    contextBuilder contextbuilder.ContextBuilder
    emitter        *emitter.ReportEmitter
}

func New(podLister v1lister.PodLister, rsLister appsv1lister.ReplicaSetLister, cb contextbuilder.ContextBuilder, e *emitter.ReportEmitter) *IncidentDetector {
    return &IncidentDetector{
        podLister:      podLister,
        rsLister:       rsLister,
        contextBuilder: cb,
        emitter:        e,
    }
}

func (d *IncidentDetector) OnEvent(event *corev1.Event) {
    if event.Reason != "OOMKilling" {
        return
    }

    pod, err := d.podLister.Pods(event.InvolvedObject.Namespace).Get(event.InvolvedObject.Name)
    if err != nil {
        fmt.Printf("[WARN] pod not found in cache: %s/%s\n",
            event.InvolvedObject.Namespace,
            event.InvolvedObject.Name,
        )
        return
    }

    d.buildAndEmit(pod, event)
}

func (d *IncidentDetector) OnPodUpdate(old, new *corev1.Pod) {
    for _, newCs := range new.Status.ContainerStatuses {
        if newCs.LastTerminationState.Terminated == nil ||
            newCs.LastTerminationState.Terminated.Reason != "OOMKilled" {
            continue
        }

        for _, oldCs := range old.Status.ContainerStatuses {
            if oldCs.Name == newCs.Name && oldCs.RestartCount == newCs.RestartCount {
                return
            }
        }

        d.buildAndEmit(new, nil)
        return
    }
}

func (d *IncidentDetector) buildAndEmit(pod *corev1.Pod, event *corev1.Event) {
    ctx, err := d.contextBuilder.Build(pod, event, d.rsLister)
    if err != nil {
        fmt.Printf("[ERROR] failed to build context: %v\n", err)
        return
    }
    // W2 console output kept for local dev visibility
    fmt.Printf("[INCIDENT] %+v\n", ctx)
    d.emitter.Emit(ctx)
}
