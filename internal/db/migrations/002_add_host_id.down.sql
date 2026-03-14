DROP INDEX IF EXISTS idx_sandboxes_host_id;

ALTER TABLE sandboxes
DROP COLUMN IF EXISTS host_id;
