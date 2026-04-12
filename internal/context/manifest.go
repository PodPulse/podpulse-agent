package context

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

// ManifestContext captures GitOps and Helm metadata from pod labels and annotations.
// All fields are best-effort — absent keys produce zero values, never errors.
type ManifestContext struct {
	GitOpsTool      string // "argocd" | "flux" | ""
	GitOpsAppName   string // value of the ArgoCD or Flux app-name label/annotation
	IsHelmManaged   bool
	HelmReleaseName string // app.kubernetes.io/instance
	HelmChart       string // helm.sh/chart
	HelmNamespace   string // app.kubernetes.io/namespace
	// ArgoCD-specific enrichment — populated when tracking-id annotation is present.
	ArgoResourceName string // e.g. "oom-argo"   — extracted from tracking-id {ns}/{name}
	ArgoResourceKind string // e.g. "Deployment" — extracted from tracking-id {group}/{kind}
	ArgoAppPath      string // e.g. "argo/"      — read from Application CR spec.source.path
}

// ManifestContextBuilder reads pod metadata to populate ManifestContext.
// It is safe for concurrent use.
type ManifestContextBuilder struct {
	dynamicClient dynamic.Interface // may be nil — ArgoCD CR lookup is skipped when nil
	argoNamespace string            // namespace where ArgoCD installs Application CRs (default: "argocd")
}

// NewManifestContextBuilder creates a builder.
// dynamicClient may be nil; if nil, ArgoAppPath will always be empty.
// argoNamespace defaults to "argocd" when empty.
func NewManifestContextBuilder(dynamicClient dynamic.Interface, argoNamespace string) *ManifestContextBuilder {
	if argoNamespace == "" {
		argoNamespace = "argocd"
	}
	return &ManifestContextBuilder{
		dynamicClient: dynamicClient,
		argoNamespace: argoNamespace,
	}
}

const (
	keyArgoCDTrackingID = "argocd.argoproj.io/tracking-id"
	keyArgoCDApp        = "argocd.argoproj.io/app-name"
	keyFluxApp          = "kustomize.toolkit.fluxcd.io/name"
	keyManagedBy        = "app.kubernetes.io/managed-by"
	keyInstance         = "app.kubernetes.io/instance"
	keyHelmChart        = "helm.sh/chart"
	keyK8sNS            = "app.kubernetes.io/namespace"
)

// Build extracts GitOps and Helm metadata from the pod's labels and annotations.
// It calls the Kubernetes API (via the dynamic client) to read the ArgoCD Application CR
// for spec.source.path. The call is best-effort — any error leaves ArgoAppPath empty.
func (b *ManifestContextBuilder) Build(ctx context.Context, pod *corev1.Pod) ManifestContext {
	labels := pod.Labels
	annotations := pod.Annotations
	mc := ManifestContext{}

	// --- ArgoCD detection (priority: tracking-id > app-name label/annotation) ---
	if trackingID, ok := annotations[keyArgoCDTrackingID]; ok && trackingID != "" {
		mc.GitOpsTool = "argocd"
		mc.ArgoResourceName, mc.ArgoResourceKind, mc.GitOpsAppName = parseArgoTrackingID(trackingID)
	} else if v := labelOrAnnotation(labels, annotations, keyArgoCDApp); v != "" {
		mc.GitOpsTool = "argocd"
		mc.GitOpsAppName = v
	} else if v := labelOrAnnotation(labels, annotations, keyFluxApp); v != "" {
		mc.GitOpsTool = "flux"
		mc.GitOpsAppName = v
	}

	// --- ArgoCD Application CR lookup for spec.source.path ---
	if mc.GitOpsTool == "argocd" && mc.GitOpsAppName != "" && b.dynamicClient != nil {
		argoCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		mc.ArgoAppPath = b.readArgoCDAppPath(argoCtx, mc.GitOpsAppName)
	}

	// --- Helm metadata lives exclusively in labels ---
	if labels[keyManagedBy] == "Helm" {
		mc.IsHelmManaged = true
	}
	mc.HelmReleaseName = labels[keyInstance]
	mc.HelmChart = labels[keyHelmChart]
	mc.HelmNamespace = labels[keyK8sNS]

	return mc
}

// parseArgoTrackingID parses the ArgoCD tracking-id annotation value.
// Format: {appName}:{group}/{kind}:{namespace}/{resourceName}
// Returns (resourceName, resourceKind, appName).
// All return values are empty strings on malformed input.
func parseArgoTrackingID(trackingID string) (resourceName, resourceKind, appName string) {
	// Split into exactly 3 parts: appName | group/kind | namespace/name
	parts := strings.SplitN(trackingID, ":", 3)
	if len(parts) != 3 {
		return "", "", ""
	}

	appName = parts[0]

	// parts[1] = "apps/Deployment" or "/Service" (empty group for core resources)
	groupKindParts := strings.Split(parts[1], "/")
	resourceKind = groupKindParts[len(groupKindParts)-1]

	// parts[2] = "namespace/resourceName"
	nsNameParts := strings.Split(parts[2], "/")
	resourceName = nsNameParts[len(nsNameParts)-1]

	return resourceName, resourceKind, appName
}

// readArgoCDAppPath reads the ArgoCD Application CR and returns spec.source.path.
// Returns "" on any error (CR not found, permission denied, etc.).
func (b *ManifestContextBuilder) readArgoCDAppPath(ctx context.Context, appName string) string {
	gvr := schema.GroupVersionResource{
		Group:    "argoproj.io",
		Version:  "v1alpha1",
		Resource: "applications",
	}

	obj, err := b.dynamicClient.Resource(gvr).Namespace(b.argoNamespace).Get(ctx, appName, metav1.GetOptions{})
	if err != nil {
		fmt.Printf("[DEBUG] ArgoCD Application CR %q not found in namespace %q: %v\n",
			appName, b.argoNamespace, err)
		return ""
	}

	spec, ok := obj.Object["spec"].(map[string]interface{})
	if !ok {
		return ""
	}
	source, ok := spec["source"].(map[string]interface{})
	if !ok {
		return ""
	}
	path, _ := source["path"].(string)
	return path
}

// labelOrAnnotation returns the value for key from labels first, then annotations.
func labelOrAnnotation(labels, annotations map[string]string, key string) string {
	if v, ok := labels[key]; ok {
		return v
	}
	if v, ok := annotations[key]; ok {
		return v
	}
	return ""
}
