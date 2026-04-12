package emitter

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	incidentcontext "github.com/PodPulse/podpulse-agent/internal/context"
	pb "github.com/PodPulse/podpulse-agent/proto/podpulse/v1"
)

const (
	maxRetries     = 3
	baseRetryDelay = 500 * time.Millisecond
)

type ReportEmitter struct {
	client pb.IncidentServiceClient
	apiKey string
}

func New(backendAddr string, apiKey string, insecureMode bool) (*ReportEmitter, error) {
	var creds credentials.TransportCredentials
	if insecureMode {
		creds = insecure.NewCredentials()
	} else {
		creds = credentials.NewTLS(&tls.Config{})
	}
	conn, err := grpc.NewClient(
		backendAddr,
		grpc.WithTransportCredentials(creds),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to backend at %s: %w", backendAddr, err)
	}

	return &ReportEmitter{
		client: pb.NewIncidentServiceClient(conn),
		apiKey: apiKey,
	}, nil
}

func (e *ReportEmitter) Emit(ctx *incidentcontext.IncidentContext) {
	report := &pb.IncidentReport{
		IncidentId:      generateID(ctx),
		IncidentType:    ctx.IncidentType,
		Namespace:       ctx.Namespace,
		PodName:         ctx.PodName,
		NodeName:        ctx.NodeName,
		RestartCount:    ctx.RestartCount,
		RawContext:      buildRawContext(ctx),
		DetectedAt:      time.Now().UTC().Unix(),
		PreviousLogTail: ctx.PreviousLogTail,
		MemoryRequest:   ctx.MemoryRequest,
		CpuLimit:        ctx.CPULimit,
		CpuRequest:      ctx.CPURequest,
		ManifestContext: buildProtoManifestContext(&ctx.ManifestCtx),
		DeployContext:   buildProtoDeployContext(&ctx.DeployCtx),
	}

	// Attach API key to every gRPC call via metadata
	outCtx := metadata.NewOutgoingContext(
		context.Background(),
		metadata.Pairs("x-api-key", e.apiKey),
	)

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			delay := time.Duration(math.Pow(2, float64(attempt))) * baseRetryDelay
			fmt.Printf("[INFO] retry attempt %d/%d after %s\n", attempt+1, maxRetries, delay)
			time.Sleep(delay)
		}

		_, err := e.client.ReportIncident(outCtx, report)
		if err == nil {
			fmt.Printf("[INFO] incident report sent: %s\n", report.IncidentId)
			return
		}
		lastErr = err
	}

	fmt.Printf("[ERROR] failed to send incident report after %d attempts: %v\n", maxRetries, lastErr)
}

// generateID produces a deterministic incident ID from namespace + pod + timestamp
func generateID(ctx *incidentcontext.IncidentContext) string {
	return fmt.Sprintf("%s-%s-%d", ctx.Namespace, ctx.PodName, time.Now().UnixNano())
}

// buildRawContext serializes the relevant scalar fields as a JSON string for
// backward compatibility with backends that read raw_context.
func buildRawContext(ctx *incidentcontext.IncidentContext) string {
	logTailJSON, _ := json.Marshal(ctx.LogTail)
	return fmt.Sprintf(
		`{"container":"%s","restart_count":%d,"memory_limit":"%s","memory_used_at_kill":"%s","owner_kind":"%s","owner_name":"%s","log_tail":%s}`,
		ctx.ContainerName,
		ctx.RestartCount,
		ctx.MemoryLimit,
		ctx.MemoryUsedAtKill,
		ctx.OwnerKind,
		ctx.OwnerName,
		string(logTailJSON),
	)
}

func buildProtoManifestContext(mc *incidentcontext.ManifestContext) *pb.ManifestContext {
	return &pb.ManifestContext{
		GitOpsTool:       mc.GitOpsTool,
		GitOpsAppName:    mc.GitOpsAppName,
		IsHelmManaged:    mc.IsHelmManaged,
		HelmReleaseName:  mc.HelmReleaseName,
		HelmChart:        mc.HelmChart,
		HelmNamespace:    mc.HelmNamespace,
		ArgoResourceName: mc.ArgoResourceName,
		ArgoResourceKind: mc.ArgoResourceKind,
		ArgoAppPath:      mc.ArgoAppPath,
	}
}

func buildProtoDeployContext(dc *incidentcontext.DeployContext) *pb.DeployContext {
	deploys := make([]*pb.RecentDeploy, 0, len(dc.RecentDeploys))
	for _, d := range dc.RecentDeploys {
		deploys = append(deploys, &pb.RecentDeploy{
			DeployedAt:     d.DeployedAt.UTC().Unix(),
			ImageTag:       d.ImageTag,
			ReplicaSetName: d.ReplicaSetName,
		})
	}
	return &pb.DeployContext{RecentDeploys: deploys}
}
