package emitter

import (
    "context"
    "fmt"
    "math"
    "time"

    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    pb "github.com/PodPulse/podpulse-agent/proto/podpulse/v1"
    incidentcontext "github.com/PodPulse/podpulse-agent/internal/context"
)

const (
    maxRetries     = 3
    baseRetryDelay = 500 * time.Millisecond
)

type ReportEmitter struct {
    client pb.IncidentServiceClient
}

func New(backendAddr string) (*ReportEmitter, error) {
    conn, err := grpc.NewClient(
        backendAddr,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
    )
    if err != nil {
        return nil, fmt.Errorf("failed to connect to backend at %s: %w", backendAddr, err)
    }

    return &ReportEmitter{
        client: pb.NewIncidentServiceClient(conn),
    }, nil
}

func (e *ReportEmitter) Emit(ctx *incidentcontext.IncidentContext) {
    report := &pb.IncidentReport{
        IncidentId:   generateID(ctx),
        IncidentType: ctx.IncidentType,
        Namespace:    ctx.Namespace,
        PodName:      ctx.PodName,
        NodeName:     ctx.NodeName,
        RestartCount: ctx.RestartCount,
        RawContext:   buildRawContext(ctx),
        DetectedAt:   time.Now().UTC().Unix(),
    }

    var lastErr error
    for attempt := 0; attempt < maxRetries; attempt++ {
        if attempt > 0 {
            delay := time.Duration(math.Pow(2, float64(attempt))) * baseRetryDelay
            fmt.Printf("[INFO] retry attempt %d/%d after %s\n", attempt+1, maxRetries, delay)
            time.Sleep(delay)
        }

        _, err := e.client.ReportIncident(context.Background(), report)
        if err == nil {
            fmt.Printf("[INFO] incident report sent: %s\n", report.IncidentId)
            return
        }
        lastErr = err
    }

    // All retries exhausted — log and continue, never crash the agent
    fmt.Printf("[ERROR] failed to send incident report after %d attempts: %v\n", maxRetries, lastErr)
}

// generateID produces a deterministic incident ID from namespace + pod + timestamp
func generateID(ctx *incidentcontext.IncidentContext) string {
    return fmt.Sprintf("%s-%s-%d", ctx.Namespace, ctx.PodName, time.Now().UnixNano())
}

// buildRawContext serializes the relevant fields as a JSON string
func buildRawContext(ctx *incidentcontext.IncidentContext) string {
    return fmt.Sprintf(
        `{"container":"%s","restart_count":%d,"memory_limit":"%s","memory_used_at_kill":"%s","owner_kind":"%s","owner_name":"%s"}`,
        ctx.ContainerName,
        ctx.RestartCount,
        ctx.MemoryLimit,
        ctx.MemoryUsedAtKill,
        ctx.OwnerKind,
        ctx.OwnerName,
    )
}