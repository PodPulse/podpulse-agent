package context

import (
	"fmt"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	appsv1lister "k8s.io/client-go/listers/apps/v1"
)

// DeployContext holds recent deploy history for the pod's owner Deployment.
type DeployContext struct {
	RecentDeploys []RecentDeploy
}

// RecentDeploy represents a single rollout as captured by its ReplicaSet.
type RecentDeploy struct {
	DeployedAt     time.Time
	ImageTag       string
	ReplicaSetName string
}

// DeployContextBuilder lists recent ReplicaSets for a pod's owner Deployment.
// It is safe for concurrent use once constructed.
type DeployContextBuilder struct {
	rsLister appsv1lister.ReplicaSetLister
}

func NewDeployContextBuilder(rsLister appsv1lister.ReplicaSetLister) *DeployContextBuilder {
	return &DeployContextBuilder{rsLister: rsLister}
}

// Build returns the 3 most-recent ReplicaSets owned by the pod's Deployment,
// sorted newest-first. Returns an empty DeployContext when the pod has no
// Deployment owner or when no matching ReplicaSets can be found.
func (b *DeployContextBuilder) Build(pod *corev1.Pod, deploymentName string) DeployContext {
	if deploymentName == "" {
		return DeployContext{}
	}

	rss, err := b.rsLister.ReplicaSets(pod.Namespace).List(labels.Everything())
	if err != nil {
		fmt.Printf("[WARN] failed to list ReplicaSets in %s: %v\n", pod.Namespace, err)
		return DeployContext{}
	}

	// Filter to RSes directly owned by the target Deployment.
	// OwnerReferences is the canonical Kubernetes ownership mechanism — more
	// reliable than label matching which can vary by toolchain.
	var owned []struct {
		name      string
		createdAt time.Time
		image     string
	}
	for _, rs := range rss {
		for _, ref := range rs.OwnerReferences {
			if ref.Kind == "Deployment" && ref.Name == deploymentName {
				imageTag := "latest"
				if len(rs.Spec.Template.Spec.Containers) > 0 {
					imageTag = extractTag(rs.Spec.Template.Spec.Containers[0].Image)
				}
				owned = append(owned, struct {
					name      string
					createdAt time.Time
					image     string
				}{
					name:      rs.Name,
					createdAt: rs.CreationTimestamp.Time,
					image:     imageTag,
				})
				break
			}
		}
	}

	// Sort descending by creation time (most recent first).
	sort.Slice(owned, func(i, j int) bool {
		return owned[i].createdAt.After(owned[j].createdAt)
	})

	const maxDeploys = 3
	if len(owned) > maxDeploys {
		owned = owned[:maxDeploys]
	}

	deploys := make([]RecentDeploy, 0, len(owned))
	for _, o := range owned {
		deploys = append(deploys, RecentDeploy{
			DeployedAt:     o.createdAt,
			ImageTag:       o.image,
			ReplicaSetName: o.name,
		})
	}

	return DeployContext{RecentDeploys: deploys}
}

// extractTag returns everything after ':' in an image reference, or "latest" when
// no tag separator is present or the tag portion is empty.
func extractTag(image string) string {
	// Strip digest if present (image@sha256:...) — use tag portion only.
	image = strings.SplitN(image, "@", 2)[0]
	parts := strings.SplitN(image, ":", 2)
	if len(parts) == 2 && parts[1] != "" {
		return parts[1]
	}
	return "latest"
}
