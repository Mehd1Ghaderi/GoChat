CREATE TABLE IF NOT EXISTS messages (
    channel_id uuid,
    bucket int,
    id timeuuid,
    user_id uuid,
    content text,
    attachments list<uuid>,
    edited_at timestamp,
    PRIMARY KEY ((channel_id, bucket), id)
) WITH CLUSTERING ORDER BY (id DESC);

CREATE TABLE IF NOT EXISTS attachments (
    id uuid PRIMARY KEY,
    channel_id uuid,
    uploader_id uuid,
    name text,
    content_type text,
    size bigint,
    object_key text,
    message_id timeuuid,
    created_at timestamp
);

CREATE TABLE IF NOT EXISTS pending_attachments (
    day int,
    id uuid,
    created_at timestamp,
    PRIMARY KEY (day, id)
);

CREATE TABLE IF NOT EXISTS message_buckets (
    channel_id uuid,
    bucket int,
    PRIMARY KEY (channel_id, bucket)
) WITH CLUSTERING ORDER BY (bucket DESC);
