-- sandboxes table
CREATE TABLE IF NOT EXISTS sandboxes (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL,
    status VARCHAR(50) NOT NULL DEFAULT 'creating',
    container_id VARCHAR(255),
    image VARCHAR(500) NOT NULL,
    workspace_mount VARCHAR(500),
    devcontainer_config JSONB,
    env_vars JSONB,
    last_activity TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at TIMESTAMPTZ,
    deleted_at TIMESTAMPTZ
);

-- port_mappings table
CREATE TABLE IF NOT EXISTS port_mappings (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sandbox_id UUID REFERENCES sandboxes(id) ON DELETE CASCADE,
    host_port INTEGER NOT NULL,
    container_port INTEGER NOT NULL,
    UNIQUE(sandbox_id, container_port)
);

-- activity_logs table (optional, for audit)
CREATE TABLE IF NOT EXISTS activity_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    sandbox_id UUID REFERENCES sandboxes(id) ON DELETE CASCADE,
    action VARCHAR(100) NOT NULL,
    details JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Index for cleanup worker queries
CREATE INDEX IF NOT EXISTS idx_sandboxes_status_activity
ON sandboxes(status, last_activity)
WHERE status = 'running';

