package collector

import (
	"context"
	"fmt"
	"io"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

// LogCollector fetches the last N lines of logs from a (previous) container instance.
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

// Collect fetches the last c.maxLines log lines from the previous (crashed) instance
// of the named container. Returns an empty string on any error — the caller must never
// rely on logs being present.
func (c *LogCollector) Collect(ctx context.Context, namespace, podName, containerName string) string {
	tailLines := int64(c.maxLines)
	req := c.clientset.CoreV1().Pods(namespace).GetLogs(podName, &corev1.PodLogOptions{
		Container: containerName,
		TailLines: &tailLines,
		Previous:  true, // fetch logs from the previous (crashed) container instance
	})

	stream, err := req.Stream(ctx)
	if err != nil {
		fmt.Printf("[WARN] failed to open log stream for %s/%s (container %s): %v\n",
			namespace, podName, containerName, err)
		return ""
	}
	defer stream.Close()

	data, err := io.ReadAll(stream)
	if err != nil {
		fmt.Printf("[WARN] failed to read log stream for %s/%s (container %s): %v\n",
			namespace, podName, containerName, err)
		return ""
	}

	return string(data)
}
