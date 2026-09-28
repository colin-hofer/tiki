-- The separate comments table was never deployed. Discard that local-only data.
DELETE FROM activity WHERE kind = 'comment.created';
DROP TABLE comments;
ALTER TABLE activity ADD COLUMN client_id TEXT;
CREATE UNIQUE INDEX activity_client ON activity(item_id, actor_id, client_id) WHERE client_id IS NOT NULL;
