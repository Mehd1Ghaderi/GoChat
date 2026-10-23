CREATE TABLE IF NOT EXISTS pending_uploads (
    day int,
    shard int,
    id uuid,
    object_key text,
    created_at timestamp,
    PRIMARY KEY ((day, shard), id)
) WITH default_time_to_live = 1209600;

DROP TABLE IF EXISTS pending_attachments;
