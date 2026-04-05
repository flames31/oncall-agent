FROM golang:1.25.0-alpine AS builder

WORKDIR /app

# Download dependencies first so this layer is cached
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /oncall-agent ./cmd/agent

# ── Runtime image ──────────────────────────────────────────────────────
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app
COPY --from=builder /oncall-agent .
COPY config.yaml .

EXPOSE 8080
ENTRYPOINT ["./oncall-agent"]