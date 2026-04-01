FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o server ./cmd/server

# Build the guest agent for linux (same arch as the host for now)
RUN CGO_ENABLED=0 GOOS=linux go build -o monza-agent ./cmd/agent

FROM alpine:3.20

RUN apk add --no-cache e2fsprogs

WORKDIR /app

COPY --from=builder /app/server /app/server
COPY --from=builder /app/monza-agent /app/monza-agent
COPY --from=builder /app/internal/db/migrations /app/internal/db/migrations
COPY --from=builder /app/internal/microvm/assets/init /app/assets/init
COPY --from=builder /app/devcontainers /app/devcontainers

ENV SERVER_ADDR=":8080"
ENV DEVCONTAINERS_PATH="/app/devcontainers"
ENV MIGRATIONS_PATH="file:///app/internal/db/migrations"
ENV MONZA_AGENT_BIN="/app/monza-agent"
ENV MONZA_INIT_SCRIPT="/app/assets/init"

EXPOSE 8080

ENTRYPOINT ["/app/server"]
