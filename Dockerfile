FROM golang:1.25-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-w -s" \
    -o podpulse-agent \
    ./cmd/agent

FROM gcr.io/distroless/static-debian12

COPY --from=builder /app/podpulse-agent /podpulse-agent

USER 65532:65532

ENTRYPOINT ["/podpulse-agent"]