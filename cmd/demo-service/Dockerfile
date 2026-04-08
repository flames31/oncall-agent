FROM golang:1.25.0-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /demo-service ./cmd/demo-service

FROM alpine:3.19
RUN apk --no-cache add ca-certificates

WORKDIR /app
COPY --from=builder /demo-service .

EXPOSE 9001
ENTRYPOINT ["./demo-service"]