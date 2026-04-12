package context

import (
	"context"
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	appsv1lister "k8s.io/client-go/listers/apps/v1"
	"k8s.io/client-go/tools/cache"
)

// ---- helpers ----

func newFakeRSLister(rss []*appsv1.ReplicaSet) appsv1lister.ReplicaSetLister {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{
		cache.NamespaceIndex: cache.MetaNamespaceIndexFunc,
	})
	for _, rs := range rss {
		_ = indexer.Add(rs)
	}
	return appsv1lister.NewReplicaSetLister(indexer)
}

func newFakeRSListerEmpty() appsv1lister.ReplicaSetLister {
	return newFakeRSLister(nil)
}

// newBuilder returns a ManifestContextBuilder with no dynamic client (ArgoAppPath always "").
func newBuilder() *ManifestContextBuilder {
	return NewManifestContextBuilder(nil, "argocd")
}

// makeRS creates a ReplicaSet owned by a Deployment with the given image and creation time.
func makeRS(namespace, name, deploymentName, image string, createdAt time.Time) *appsv1.ReplicaSet {
	return &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         namespace,
			CreationTimestamp: metav1.Time{Time: createdAt},
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: deploymentName},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "app", Image: image},
					},
				},
			},
		},
	}
}

// ---- ManifestContextBuilder tests ----

func TestManifestContextBuilder_ArgoCD_LegacyAppNameLabel(t *testing.T) {
	// Old ArgoCD (≤2.3): uses argocd.argoproj.io/app-name label, no tracking-id.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"argocd.argoproj.io/app-name":  "payment-api",
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/instance":   "payment-api",
				"helm.sh/chart":                "payment-api-1.2.3",
				"app.kubernetes.io/namespace":  "payments",
			},
		},
	}

	mc := newBuilder().Build(context.Background(), pod)

	if mc.GitOpsTool != "argocd" {
		t.Errorf("GitOpsTool: got %q, want %q", mc.GitOpsTool, "argocd")
	}
	if mc.GitOpsAppName != "payment-api" {
		t.Errorf("GitOpsAppName: got %q, want %q", mc.GitOpsAppName, "payment-api")
	}
	if mc.ArgoResourceName != "" {
		t.Errorf("ArgoResourceName: expected empty (no tracking-id), got %q", mc.ArgoResourceName)
	}
	if mc.ArgoResourceKind != "" {
		t.Errorf("ArgoResourceKind: expected empty (no tracking-id), got %q", mc.ArgoResourceKind)
	}
	if !mc.IsHelmManaged {
		t.Error("IsHelmManaged: got false, want true")
	}
	if mc.HelmReleaseName != "payment-api" {
		t.Errorf("HelmReleaseName: got %q, want %q", mc.HelmReleaseName, "payment-api")
	}
	if mc.HelmChart != "payment-api-1.2.3" {
		t.Errorf("HelmChart: got %q, want %q", mc.HelmChart, "payment-api-1.2.3")
	}
	if mc.HelmNamespace != "payments" {
		t.Errorf("HelmNamespace: got %q, want %q", mc.HelmNamespace, "payments")
	}
}

func TestManifestContextBuilder_ArgoCD_LegacyAppNameAnnotation(t *testing.T) {
	// ArgoCD app-name in annotation (≤2.3 annotation style).
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      map[string]string{},
			Annotations: map[string]string{"argocd.argoproj.io/app-name": "my-app"},
		},
	}

	mc := newBuilder().Build(context.Background(), pod)

	if mc.GitOpsTool != "argocd" {
		t.Errorf("GitOpsTool: got %q, want %q", mc.GitOpsTool, "argocd")
	}
	if mc.GitOpsAppName != "my-app" {
		t.Errorf("GitOpsAppName: got %q, want %q", mc.GitOpsAppName, "my-app")
	}
	if mc.ArgoResourceName != "" || mc.ArgoResourceKind != "" {
		t.Error("ArgoResourceName/Kind: expected empty (no tracking-id)")
	}
}

func TestManifestContextBuilder_ArgoCD_TrackingIDAnnotation(t *testing.T) {
	// ArgoCD v2.4+ annotation mode: tracking-id annotation present.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{},
			Annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "podpulse-argo-test:apps/Deployment:podpulse-test/oom-argo",
			},
		},
	}

	mc := newBuilder().Build(context.Background(), pod)

	if mc.GitOpsTool != "argocd" {
		t.Errorf("GitOpsTool: got %q, want argocd", mc.GitOpsTool)
	}
	if mc.GitOpsAppName != "podpulse-argo-test" {
		t.Errorf("GitOpsAppName: got %q, want podpulse-argo-test", mc.GitOpsAppName)
	}
	if mc.ArgoResourceName != "oom-argo" {
		t.Errorf("ArgoResourceName: got %q, want oom-argo", mc.ArgoResourceName)
	}
	if mc.ArgoResourceKind != "Deployment" {
		t.Errorf("ArgoResourceKind: got %q, want Deployment", mc.ArgoResourceKind)
	}
}

func TestManifestContextBuilder_ArgoCD_TrackingIDPluslabel(t *testing.T) {
	// ArgoCD v3.0 annotation+label mode: both tracking-id annotation AND instance label.
	// tracking-id takes priority over legacy app-name label.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"app.kubernetes.io/instance": "podpulse-argo-test",
			},
			Annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "podpulse-argo-test:apps/Deployment:podpulse-test/oom-argo",
			},
		},
	}

	mc := newBuilder().Build(context.Background(), pod)

	if mc.GitOpsTool != "argocd" {
		t.Errorf("GitOpsTool: got %q, want argocd", mc.GitOpsTool)
	}
	if mc.GitOpsAppName != "podpulse-argo-test" {
		t.Errorf("GitOpsAppName: got %q, want podpulse-argo-test", mc.GitOpsAppName)
	}
	if mc.ArgoResourceName != "oom-argo" {
		t.Errorf("ArgoResourceName: got %q, want oom-argo", mc.ArgoResourceName)
	}
	if mc.ArgoResourceKind != "Deployment" {
		t.Errorf("ArgoResourceKind: got %q, want Deployment", mc.ArgoResourceKind)
	}
}

func TestManifestContextBuilder_ArgoCD_TrackingID_CoreGroup(t *testing.T) {
	// Core-group resource (e.g. Service): group is empty → "/Service"
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "my-app:/Service:default/my-svc",
			},
		},
	}

	mc := newBuilder().Build(context.Background(), pod)

	if mc.GitOpsAppName != "my-app" {
		t.Errorf("GitOpsAppName: got %q, want my-app", mc.GitOpsAppName)
	}
	if mc.ArgoResourceName != "my-svc" {
		t.Errorf("ArgoResourceName: got %q, want my-svc", mc.ArgoResourceName)
	}
	if mc.ArgoResourceKind != "Service" {
		t.Errorf("ArgoResourceKind: got %q, want Service", mc.ArgoResourceKind)
	}
}

func TestManifestContextBuilder_ArgoCD_TrackingID_Malformed(t *testing.T) {
	// Malformed tracking-id — fields should be empty, no panic.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				"argocd.argoproj.io/tracking-id": "not-a-valid-tracking-id",
			},
		},
	}

	mc := newBuilder().Build(context.Background(), pod)

	// ArgoCD tool is NOT detected — malformed tracking-id has no ':' separators.
	// GitOpsTool will be empty since GitOpsAppName would also be empty.
	if mc.ArgoResourceName != "" || mc.ArgoResourceKind != "" {
		t.Errorf("expected empty ArgoResourceName/Kind for malformed tracking-id, got name=%q kind=%q",
			mc.ArgoResourceName, mc.ArgoResourceKind)
	}
}

func TestManifestContextBuilder_Flux(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"kustomize.toolkit.fluxcd.io/name": "infra-apps",
			},
		},
	}

	mc := newBuilder().Build(context.Background(), pod)

	if mc.GitOpsTool != "flux" {
		t.Errorf("GitOpsTool: got %q, want %q", mc.GitOpsTool, "flux")
	}
	if mc.GitOpsAppName != "infra-apps" {
		t.Errorf("GitOpsAppName: got %q, want %q", mc.GitOpsAppName, "infra-apps")
	}
	if mc.IsHelmManaged {
		t.Error("IsHelmManaged: got true, want false")
	}
}

func TestManifestContextBuilder_HelmOnly(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "Helm",
				"app.kubernetes.io/instance":   "my-release",
				"helm.sh/chart":                "nginx-1.0.0",
			},
		},
	}

	mc := newBuilder().Build(context.Background(), pod)

	if mc.GitOpsTool != "" {
		t.Errorf("GitOpsTool: got %q, want empty", mc.GitOpsTool)
	}
	if !mc.IsHelmManaged {
		t.Error("IsHelmManaged: got false, want true")
	}
	if mc.HelmReleaseName != "my-release" {
		t.Errorf("HelmReleaseName: got %q, want %q", mc.HelmReleaseName, "my-release")
	}
}

func TestManifestContextBuilder_NoAnnotations(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      map[string]string{"app": "my-app"},
			Annotations: map[string]string{},
		},
	}

	mc := newBuilder().Build(context.Background(), pod)

	if mc.GitOpsTool != "" {
		t.Errorf("GitOpsTool: got %q, want empty", mc.GitOpsTool)
	}
	if mc.GitOpsAppName != "" {
		t.Errorf("GitOpsAppName: got %q, want empty", mc.GitOpsAppName)
	}
	if mc.IsHelmManaged {
		t.Error("IsHelmManaged: got true, want false")
	}
	if mc.HelmReleaseName != "" || mc.HelmChart != "" || mc.HelmNamespace != "" {
		t.Error("Helm fields: expected all empty")
	}
}

// ---- parseArgoTrackingID unit tests ----

func TestParseArgoTrackingID_AppsGroup(t *testing.T) {
	name, kind, app := parseArgoTrackingID("podpulse-argo-test:apps/Deployment:podpulse-test/oom-argo")
	if app != "podpulse-argo-test" {
		t.Errorf("appName: got %q, want podpulse-argo-test", app)
	}
	if kind != "Deployment" {
		t.Errorf("kind: got %q, want Deployment", kind)
	}
	if name != "oom-argo" {
		t.Errorf("resourceName: got %q, want oom-argo", name)
	}
}

func TestParseArgoTrackingID_CoreGroup(t *testing.T) {
	name, kind, app := parseArgoTrackingID("my-app:/Service:default/my-svc")
	if app != "my-app" {
		t.Errorf("appName: got %q, want my-app", app)
	}
	if kind != "Service" {
		t.Errorf("kind: got %q, want Service", kind)
	}
	if name != "my-svc" {
		t.Errorf("resourceName: got %q, want my-svc", name)
	}
}

func TestParseArgoTrackingID_Malformed(t *testing.T) {
	name, kind, app := parseArgoTrackingID("not-valid")
	if name != "" || kind != "" || app != "" {
		t.Errorf("expected all empty for malformed input, got name=%q kind=%q app=%q", name, kind, app)
	}
}

// ---- DeployContextBuilder tests ----

func TestDeployContextBuilder_ThreeReplicaSets_SortedDescending(t *testing.T) {
	now := time.Now().UTC()
	namespace := "payments"
	deployment := "payment-api"

	rs1 := makeRS(namespace, "payment-api-aaa", deployment, "myapp:v1.0", now.Add(-2*time.Hour))
	rs2 := makeRS(namespace, "payment-api-bbb", deployment, "myapp:v1.1", now.Add(-1*time.Hour))
	rs3 := makeRS(namespace, "payment-api-ccc", deployment, "myapp:v1.2", now)

	lister := newFakeRSLister([]*appsv1.ReplicaSet{rs1, rs2, rs3})
	b := NewDeployContextBuilder(lister)

	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}
	dc := b.Build(pod, deployment)

	if len(dc.RecentDeploys) != 3 {
		t.Fatalf("expected 3 deploys, got %d", len(dc.RecentDeploys))
	}
	if dc.RecentDeploys[0].ReplicaSetName != "payment-api-ccc" {
		t.Errorf("first deploy: got %q, want payment-api-ccc", dc.RecentDeploys[0].ReplicaSetName)
	}
	if dc.RecentDeploys[1].ReplicaSetName != "payment-api-bbb" {
		t.Errorf("second deploy: got %q, want payment-api-bbb", dc.RecentDeploys[1].ReplicaSetName)
	}
	if dc.RecentDeploys[2].ReplicaSetName != "payment-api-aaa" {
		t.Errorf("third deploy: got %q, want payment-api-aaa", dc.RecentDeploys[2].ReplicaSetName)
	}
	if dc.RecentDeploys[0].ImageTag != "v1.2" {
		t.Errorf("image tag: got %q, want v1.2", dc.RecentDeploys[0].ImageTag)
	}
}

func TestDeployContextBuilder_MoreThanThree_CapAtThree(t *testing.T) {
	now := time.Now().UTC()
	namespace := "default"
	deployment := "my-app"

	rss := make([]*appsv1.ReplicaSet, 5)
	for i := range rss {
		rss[i] = makeRS(namespace, fmt.Sprintf("rs-%d", i), deployment,
			fmt.Sprintf("img:v%d", i), now.Add(time.Duration(i)*time.Hour))
	}

	lister := newFakeRSLister(rss)
	b := NewDeployContextBuilder(lister)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}
	dc := b.Build(pod, deployment)

	if len(dc.RecentDeploys) != 3 {
		t.Errorf("expected 3 deploys (capped), got %d", len(dc.RecentDeploys))
	}
}

func TestDeployContextBuilder_NoDeploymentOwner_ReturnsEmpty(t *testing.T) {
	b := NewDeployContextBuilder(newFakeRSListerEmpty())
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "default"}}
	dc := b.Build(pod, "")

	if len(dc.RecentDeploys) != 0 {
		t.Errorf("expected empty DeployContext, got %d deploys", len(dc.RecentDeploys))
	}
}

func TestDeployContextBuilder_ImageNoTag_DefaultsToLatest(t *testing.T) {
	namespace := "default"
	deployment := "my-app"
	rs := makeRS(namespace, "my-app-abc", deployment, "nginx", time.Now())

	lister := newFakeRSLister([]*appsv1.ReplicaSet{rs})
	b := NewDeployContextBuilder(lister)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}
	dc := b.Build(pod, deployment)

	if len(dc.RecentDeploys) != 1 {
		t.Fatalf("expected 1 deploy, got %d", len(dc.RecentDeploys))
	}
	if dc.RecentDeploys[0].ImageTag != "latest" {
		t.Errorf("ImageTag: got %q, want %q", dc.RecentDeploys[0].ImageTag, "latest")
	}
}

func TestDeployContextBuilder_NoMatchingRS_ReturnsEmpty(t *testing.T) {
	namespace := "default"
	rs := makeRS(namespace, "other-rs", "other-deployment", "img:v1", time.Now())
	lister := newFakeRSLister([]*appsv1.ReplicaSet{rs})
	b := NewDeployContextBuilder(lister)
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: namespace}}
	dc := b.Build(pod, "my-app")

	if len(dc.RecentDeploys) != 0 {
		t.Errorf("expected empty DeployContext, got %d deploys", len(dc.RecentDeploys))
	}
}

// ---- Container name match fix (OOMContextBuilder) ----

func TestOOMContextBuilder_ContainerMatchByName_NotByIndex(t *testing.T) {
	appMemLimit := resource.MustParse("256Mi")
	sidecarMemLimit := resource.MustParse("64Mi")

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "mypod", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
			Containers: []corev1.Container{
				{
					Name: "sidecar",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{corev1.ResourceMemory: sidecarMemLimit},
					},
				},
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{corev1.ResourceMemory: appMemLimit},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         "app",
					RestartCount: 1,
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
					},
				},
				{Name: "sidecar", RestartCount: 0},
			},
		},
	}

	builder := NewOOMContextBuilder(nil, newBuilder(), NewDeployContextBuilder(newFakeRSListerEmpty()))
	ctx, err := builder.Build(pod, nil, newFakeRSListerEmpty())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ctx.ContainerName != "app" {
		t.Errorf("ContainerName: got %q, want app", ctx.ContainerName)
	}
	if ctx.MemoryLimit != appMemLimit.String() {
		t.Errorf("MemoryLimit: got %q, want %q — index bug would return sidecar limit %q",
			ctx.MemoryLimit, appMemLimit.String(), sidecarMemLimit.String())
	}
}

func TestOOMContextBuilder_ResourceFields(t *testing.T) {
	memLimit := resource.MustParse("512Mi")
	memRequest := resource.MustParse("256Mi")
	cpuLimit := resource.MustParse("500m")
	cpuRequest := resource.MustParse("100m")

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "mypod", Namespace: "default"},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Limits: corev1.ResourceList{
							corev1.ResourceMemory: memLimit,
							corev1.ResourceCPU:    cpuLimit,
						},
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: memRequest,
							corev1.ResourceCPU:    cpuRequest,
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         "app",
					RestartCount: 2,
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"},
					},
				},
			},
		},
	}

	builder := NewOOMContextBuilder(nil, newBuilder(), NewDeployContextBuilder(newFakeRSListerEmpty()))
	ctx, err := builder.Build(pod, nil, newFakeRSListerEmpty())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if ctx.MemoryLimit != memLimit.String() {
		t.Errorf("MemoryLimit: got %q, want %q", ctx.MemoryLimit, memLimit.String())
	}
	if ctx.MemoryRequest != memRequest.String() {
		t.Errorf("MemoryRequest: got %q, want %q", ctx.MemoryRequest, memRequest.String())
	}
	if ctx.CPULimit != cpuLimit.String() {
		t.Errorf("CPULimit: got %q, want %q", ctx.CPULimit, cpuLimit.String())
	}
	if ctx.CPURequest != cpuRequest.String() {
		t.Errorf("CPURequest: got %q, want %q", ctx.CPURequest, cpuRequest.String())
	}
}

// ---- extractTag helper ----

func TestExtractTag(t *testing.T) {
	cases := []struct {
		image string
		want  string
	}{
		{"nginx", "latest"},
		{"nginx:1.25", "1.25"},
		{"myrepo/myapp:v2.0.0", "v2.0.0"},
		{"myrepo/myapp", "latest"},
		{"myapp:", "latest"},
		{"myapp@sha256:abc123", "latest"},
		{"myapp:v1@sha256:abc", "v1"},
	}

	for _, c := range cases {
		got := extractTag(c.image)
		if got != c.want {
			t.Errorf("extractTag(%q) = %q, want %q", c.image, got, c.want)
		}
	}
}
