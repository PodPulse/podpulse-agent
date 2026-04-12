package collector

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

// LogCollector fetches log lines from a container instance.
type LogCollector struct {
	clientset kubernetes.Interface
	maxLines  int
}

// New creates a LogCollector. maxLines controls how many tail lines are fetched;
// pass 50 as the default.
func New(clientset kubernetes.Interface, maxLines int) *LogCollector {
	return &LogCollector{
		clientset: clientset,
		maxLines:  maxLines,
	}
}

// Collect fetches the last c.maxLines log lines from the current (running) container
// instance. Returns empty string on any error — callers must never rely on logs being present.
func (c *LogCollector) Collect(ctx context.Context, namespace, podName, containerName string) string {
	return c.fetch(ctx, namespace, podName, containerName, false)
}

// CollectPrevious fetches the last c.maxLines log lines from the previous (crashed) container
// instance. Returns empty string if no previous container exists or on any error.
func (c *LogCollector) CollectPrevious(ctx context.Context, namespace, podName, containerName string) string {
	return c.fetch(ctx, namespace, podName, containerName, true)
}

func (c *LogCollector) fetch(ctx context.Context, namespace, podName, containerName string, previous bool) string {
	tailLines := int64(c.maxLines)
	req := c.clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: containerName,
		TailLines: &tailLines,
		Previous:  previous,
	})

	stream, err := req.Stream(ctx)
	if err != nil {
		fmt.Printf("[WARN] failed to open log stream for %s/%s (container %s, previous=%v): %v\n",
			namespace, podName, containerName, previous, err)
		return ""
	}
	defer stream.Close()

	data, err := io.ReadAll(stream)
	if err != nil {
		fmt.Printf("[WARN] failed to read log stream for %s/%s (container %s, previous=%v): %v\n",
			namespace, podName, containerName, previous, err)
		return ""
	}

	return string(data)
}
