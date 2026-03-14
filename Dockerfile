FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o server ./cmd/server

FROM alpine:3.20

WORKDIR /app

COPY --from=builder /app/server /app/server
COPY --from=builder /app/internal/db/migrations /app/internal/db/migrations
COPY --from=builder /app/devcontainers /app/devcontainers

ENV SERVER_ADDR=":8080"
ENV DEVCONTAINERS_PATH="/app/devcontainers"
ENV MIGRATIONS_PATH="file:///app/internal/db/migrations"

EXPOSE 8080

# Run as root (UID 0) so the process can connect to /var/run/docker.sock
USER 0

ENTRYPOINT ["/app/server"]

