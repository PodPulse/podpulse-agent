package detector

import (
    "fmt"

    corev1 "k8s.io/api/core/v1"
    v1lister "k8s.io/client-go/listers/core/v1"

    contextbuilder "github.com/PodPulse/podpulse-agent/internal/context"
)

type IncidentDetector struct {
    podLister      v1lister.PodLister
    contextBuilder contextbuilder.ContextBuilder
}

func New(podLister v1lister.PodLister, cb contextbuilder.ContextBuilder) *IncidentDetector {
    return &IncidentDetector{
        podLister:      podLister,
        contextBuilder: cb,
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

    d.buildAndPrint(pod, event)
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

        d.buildAndPrint(new, nil)
        return
    }
}

func (d *IncidentDetector) buildAndPrint(pod *corev1.Pod, event *corev1.Event) {
    ctx, err := d.contextBuilder.Build(pod, event)
    if err != nil {
        fmt.Printf("[ERROR] failed to build context: %v\n", err)
        return
    }
    fmt.Printf("[INCIDENT] %+v\n", ctx)
}