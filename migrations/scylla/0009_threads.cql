ALTER TABLE messages ADD reply_count int;
ALTER TABLE messages ADD last_reply_at timestamp;
ALTER TABLE messages ADD participants set<uuid>;

CREATE TABLE IF NOT EXISTS replies (
    channel_id uuid,
    root_id timeuuid,
    id timeuuid,
    user_id uuid,
    content text,
    attachments list<uuid>,
    edited_at timestamp,
    PRIMARY KEY ((channel_id, root_id), id)
) WITH CLUSTERING ORDER BY (id ASC);
