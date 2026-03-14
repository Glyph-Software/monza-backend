## Sandbox Service Overview

This backend exposes a simple sandbox management API built on:

- Docker containers created from devcontainer.json templates
- PostgreSQL for sandbox persistence and activity tracking
- A 15-minute idle timeout with automatic cleanup

### Lifecycle

1. Clients create a sandbox via `POST /api/sandboxes` with a template name.
2. The server loads the matching `devcontainers/{template}/devcontainer.json`.
3. A Docker container is created and started for the sandbox.
4. The sandbox is persisted in PostgreSQL with activity timestamps.
5. Clients periodically call `POST /api/sandboxes/{id}/heartbeat` to keep it alive.
6. A background worker expires and deletes sandboxes idle for more than 15 minutes.

### Key Components

- `internal/devcontainer` – parses devcontainer.json templates.
- `internal/docker` – wraps the Docker SDK.
- `internal/db` & `internal/db/repository` – PostgreSQL and repositories.
- `internal/sandbox` – sandbox manager, heartbeat, and cleanup worker.
- `internal/handlers/sandboxes.go` – HTTP handlers for the sandbox API.

