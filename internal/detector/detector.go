package detector

import (
	"fmt"
	"log"

	corev1 "k8s.io/api/core/v1"
	appsv1lister "k8s.io/client-go/listers/apps/v1"
	v1lister "k8s.io/client-go/listers/core/v1"

	contextbuilder "github.com/PodPulse/podpulse-agent/internal/context"
	"github.com/PodPulse/podpulse-agent/internal/emitter"
)

type IncidentDetector struct {
	podLister        v1lister.PodLister
	rsLister         appsv1lister.ReplicaSetLister
	oomBuilder       contextbuilder.ContextBuilder
	crashLoopBuilder contextbuilder.ContextBuilder
	emitter          *emitter.ReportEmitter
}

func New(
	podLister v1lister.PodLister,
	rsLister appsv1lister.ReplicaSetLister,
	oomBuilder contextbuilder.ContextBuilder,
	crashLoopBuilder contextbuilder.ContextBuilder,
	e *emitter.ReportEmitter,
) *IncidentDetector {
	return &IncidentDetector{
		podLister:        podLister,
		rsLister:         rsLister,
		oomBuilder:       oomBuilder,
		crashLoopBuilder: crashLoopBuilder,
		emitter:          e,
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

	d.buildAndEmit(pod, event, d.oomBuilder)
}

func (d *IncidentDetector) OnPodUpdate(old, new *corev1.Pod) {
	for _, newCs := range new.Status.ContainerStatuses {
		log.Printf("[DEBUG] pod=%s container=%s waitingReason=%v restartCount=%d",
			new.Name,
			newCs.Name,
			func() string {
				if newCs.State.Waiting != nil {
					return newCs.State.Waiting.Reason
				}
				return "nil"
			}(),
			newCs.RestartCount)
	}

	for _, newCs := range new.Status.ContainerStatuses {
		// OOMKilled
		if newCs.LastTerminationState.Terminated != nil &&
			newCs.LastTerminationState.Terminated.Reason == "OOMKilled" {

			for _, oldCs := range old.Status.ContainerStatuses {
				if oldCs.Name == newCs.Name && oldCs.RestartCount == newCs.RestartCount {
					return
				}
			}

			d.buildAndEmit(new, nil, d.oomBuilder)
			return
		}

		// CrashLoopBackOff
		if newCs.RestartCount >= 3 {
			lastReason := ""
			if newCs.LastTerminationState.Terminated != nil {
				lastReason = newCs.LastTerminationState.Terminated.Reason
			}

			isCrashLoop := (newCs.State.Waiting != nil && newCs.State.Waiting.Reason == "CrashLoopBackOff") ||
				(lastReason != "" && lastReason != "OOMKilled")

			if isCrashLoop {
				for _, oldCs := range old.Status.ContainerStatuses {
					if oldCs.Name == newCs.Name && oldCs.RestartCount == newCs.RestartCount {
						return
					}
				}
				d.buildAndEmit(new, nil, d.crashLoopBuilder)
				return
			}
		}
	}
}

func (d *IncidentDetector) buildAndEmit(pod *corev1.Pod, event *corev1.Event, builder contextbuilder.ContextBuilder) {
	ctx, err := builder.Build(pod, event, d.rsLister)
	if err != nil {
		fmt.Printf("[ERROR] failed to build context: %v\n", err)
		return
	}
	fmt.Printf("[INCIDENT] %+v\n", ctx)
	d.emitter.Emit(ctx)
}
