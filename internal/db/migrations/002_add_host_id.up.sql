ALTER TABLE sandboxes
ADD COLUMN IF NOT EXISTS host_id VARCHAR(255) NOT NULL DEFAULT 'default';

CREATE INDEX IF NOT EXISTS idx_sandboxes_host_id
ON sandboxes(host_id)
WHERE deleted_at IS NULL;
