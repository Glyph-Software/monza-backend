## Monza Backend

Go backend for managing short‑lived development sandboxes backed by Docker and PostgreSQL.

It exposes:

- `/health` – service health check.
- `/hello` – simple hello world endpoint.
- `/api/sandboxes` – CRUD + heartbeat API for sandbox lifecycle.

The service creates Docker containers from `devcontainer.json` templates, tracks them in PostgreSQL, and automatically cleans up idle sandboxes after a configurable TTL (15 minutes by default).

---

### Architecture

- **Entry point**
  - `cmd/server/main.go`  
    - Reads configuration (e.g. `DATABASE_URL`, `SERVER_ADDR`, `DEVCONTAINERS_PATH`).
    - Runs database migrations on startup with retry.
    - Creates the DB connection and Docker client.
    - Instantiates the sandbox `Manager` with a 15‑minute session TTL.
    - Starts the HTTP server.

- **HTTP server**
  - `internal/httpserver/server.go`, `internal/httpserver/routes.go`
    - Uses `http.ServeMux`.
    - Registers:
      - `GET /health`
      - `GET /hello`
      - `GET /api/sandboxes`
      - `POST /api/sandboxes`
      - `GET /api/sandboxes/{id}`
      - `DELETE /api/sandboxes/{id}`
      - `POST /api/sandboxes/{id}/heartbeat`

- **Handlers**
  - `internal/handlers/health.go` – JSON health response.
  - `internal/handlers/hello.go` – JSON hello response.
  - `internal/handlers/sandboxes.go` – sandbox REST API:
    - Parses and validates requests.
    - Loads devcontainer templates.
    - Delegates to the sandbox manager.
    - Returns JSON responses and errors.

- **Sandbox domain**
  - `internal/sandbox/*`, `pkg/models/sandbox.go`
    - `Manager` orchestrates:
      - Creating containers from devcontainer templates.
      - Persisting sandbox metadata.
      - Tracking last activity and expiry.
      - Deleting containers and marking sandboxes deleted/expired.
    - Background cleanup worker:
      - Runs every minute (configurable).
      - Expires and deletes idle sandboxes older than the TTL.

- **Persistence**
  - `internal/db/*`, `internal/db/repository/*`
    - PostgreSQL connection + migrations.
    - Repositories for sandboxes and port mappings.

- **Devcontainers & Docker**
  - `internal/devcontainer/*`
    - Parses `devcontainer.json` templates from:
      - `DEVCONTAINERS_PATH` (if set), or
      - local `./devcontainers` directory.
  - `internal/docker/*`
    - Thin wrapper around the Docker SDK for creating, starting, and cleaning up containers.

For a more narrative overview, also see `SANDBOX.md`.

---

### Requirements

- Go 1.22 or later
- Docker daemon reachable from the backend
- PostgreSQL database

Environment variables:

- `DATABASE_URL` – **required**, PostgreSQL connection string.
- `SERVER_ADDR` – optional, listen address (default `:8080`).
- `DEVCONTAINERS_PATH` – optional, base directory for devcontainer templates (default `devcontainers`).

---

### Running the server locally

From the `backend` directory:

```bash
# Ensure DATABASE_URL points to a running Postgres instance
export DATABASE_URL="postgres://user:password@localhost:5432/monza?sslmode=disable"

go run ./cmd/server
```

By default, the server listens on `http://localhost:8080`.

To use a custom address:

```bash
SERVER_ADDR=":9000" go run ./cmd/server
```

If you want to use a custom devcontainers directory:

```bash
DEVCONTAINERS_PATH="/absolute/path/to/devcontainers" go run ./cmd/server
```

On startup the server will:

1. Run database migrations (with retry) against `DATABASE_URL`.
2. Connect to PostgreSQL.
3. Create a Docker client.
4. Start the HTTP server and the sandbox cleanup worker.

---

### HTTP API

#### Health

- **GET `/health`**

  - **Description**: Liveness / readiness probe.
  - **Response** `200 OK`:

    ```json
    {
      "status": "ok"
    }
    ```

#### Hello

- **GET `/hello`**

  - **Description**: Simple hello world endpoint.
  - **Response** `200 OK`:

    ```json
    {
      "message": "hello, world"
    }
    ```

#### Sandbox model

Sandboxes are represented as:

```json
{
  "id": "00000000-0000-0000-0000-000000000000",
  "name": "sandbox-go",
  "status": "running",
  "container_id": "docker-container-id",
  "image": "registry/image:tag",
  "workspace_mount": "/path/in/container",
  "devcontainer_config": { /* original devcontainer.json as JSON */ },
  "env_vars": {
    "EXAMPLE": "value"
  },
  "port_mappings": [
    {
      "id": "11111111-1111-1111-1111-111111111111",
      "sandbox_id": "00000000-0000-0000-0000-000000000000",
      "host_port": 12345,
      "container_port": 8080
    }
  ],
  "last_activity": "2024-01-01T12:00:00Z",
  "created_at": "2024-01-01T12:00:00Z",
  "expires_at": "2024-01-01T12:15:00Z",
  "deleted_at": null
}
```

Key status values:

- `creating`, `running`, `expired`, `deleted`, `error`.

---

### Sandbox Endpoints

#### List sandboxes

- **GET `/api/sandboxes`**

  - **Description**: Returns all known sandboxes.
  - **Response** `200 OK`:

    ```json
    [
      {
        "id": "00000000-0000-0000-0000-000000000000",
        "name": "sandbox-go",
        "status": "running"
        // ... see full model above ...
      }
    ]
    ```

**Example:**

```bash
curl -X GET http://localhost:8080/api/sandboxes
```

#### Create sandbox

- **POST `/api/sandboxes`**

  - **Description**: Creates a new sandbox from a devcontainer template.
  - **Request body**:

    ```json
    {
      "name": "optional-sandbox-name",
      "template": "go"
    }
    ```

    - `template`:
      - Name of a directory under `DEVCONTAINERS_PATH` (or local `devcontainers`) that contains `devcontainer.json`.
      - Defaults to `"go"` if omitted or empty.
    - `name`:
      - Optional; defaults to `"sandbox-" + template` if omitted or empty.

  - **Responses**:
    - `201 Created` with sandbox JSON on success.
    - `400 Bad Request` if the body is invalid or the template cannot be loaded.
    - `500 Internal Server Error` for unexpected errors.

**Example:**

```bash
curl -X POST http://localhost:8080/api/sandboxes \
  -H "Content-Type: application/json" \
  -d '{
    "name": "sandbox-go",
    "template": "go"
  }'
```

#### Get sandbox

- **GET `/api/sandboxes/{id}`**

  - **Description**: Fetch a single sandbox by its UUID.
  - **Path params**:
    - `id` – sandbox UUID.
  - **Responses**:
    - `200 OK` with sandbox JSON.
    - `404 Not Found` if the sandbox does not exist.

**Example:**

```bash
curl -X GET http://localhost:8080/api/sandboxes/<sandbox-id>
```

#### Delete sandbox

- **DELETE `/api/sandboxes/{id}`**

  - **Description**: Deletes a sandbox and its underlying Docker container.
  - **Path params**:
    - `id` – sandbox UUID.
  - **Responses**:
    - `204 No Content` on success.
    - `500 Internal Server Error` on failure.

**Example:**

```bash
curl -X DELETE http://localhost:8080/api/sandboxes/<sandbox-id>
```

#### Heartbeat sandbox

- **POST `/api/sandboxes/{id}/heartbeat`**

  - **Description**: Marks the sandbox as active, extending its expiry.
  - **Path params**:
    - `id` – sandbox UUID.
  - **Request body**: none.
  - **Responses**:
    - `204 No Content` on success.
    - `500 Internal Server Error` on failure.

**Example:**

```bash
curl -X POST http://localhost:8080/api/sandboxes/<sandbox-id>/heartbeat
```

---

### Sandbox lifecycle

High‑level lifecycle (also described in `SANDBOX.md`):

1. **Create** – `POST /api/sandboxes` with a `template` name.
2. **Provision** – backend reads `devcontainers/{template}/devcontainer.json` (or from `DEVCONTAINERS_PATH`) and creates a Docker container.
3. **Persist** – sandbox metadata, ports, and timestamps are stored in PostgreSQL.
4. **Use** – clients connect to mapped ports (e.g. editors, terminals, HTTP services inside the container).
5. **Heartbeat** – clients periodically call `POST /api/sandboxes/{id}/heartbeat` to keep the sandbox alive.
6. **Cleanup** – background worker expires and deletes sandboxes idle longer than the configured TTL (15 minutes by default), updating their status to `expired` / `deleted`.

---

### Project layout (summary)

- `cmd/server` – main server binary.
- `internal/httpserver` – HTTP server setup and routing.
- `internal/handlers` – HTTP handlers (`/health`, `/hello`, `/api/sandboxes`).
- `internal/sandbox` – sandbox manager, heartbeat handling, cleanup worker.
- `internal/devcontainer` – devcontainer.json parsing.
- `internal/docker` – Docker client wrapper.
- `internal/db` – DB connection and migrations.
- `internal/db/repository` – database repositories.
- `pkg/models` – shared domain models (e.g. `Sandbox`).

## Go backend

Structured Go HTTP server with:

- `/health` – health check endpoint returning `{"status":"ok"}`.
- `/hello` – hello world endpoint returning `{"message":"hello, world"}`.

### Project layout

- `cmd/server` – main entrypoint binary.
- `internal/httpserver` – HTTP server wiring and routing.
- `internal/handlers` – individual HTTP handlers and response helpers.

### Requirements

- Go 1.22 or later

### Run the server

```bash
cd backend
go run ./cmd/server
```

By default the server listens on `http://localhost:8080`.  
You can override the address with the `SERVER_ADDR` environment variable, for example:

```bash
SERVER_ADDR=":9000" go run ./cmd/server
```

### Test the endpoints

```bash
curl http://localhost:8080/health
curl http://localhost:8080/hello
```

