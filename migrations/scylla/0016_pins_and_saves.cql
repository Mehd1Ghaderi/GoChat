ALTER TABLE messages ADD pinned boolean;
ALTER TABLE replies ADD pinned boolean;

CREATE TABLE IF NOT EXISTS pinned_messages (
    channel_id uuid,
    message_id timeuuid,
    root_id timeuuid,
    pinned_by uuid,
    PRIMARY KEY (channel_id, message_id)
) WITH CLUSTERING ORDER BY (message_id DESC);

CREATE TABLE IF NOT EXISTS saved_messages (
    user_id uuid,
    message_id timeuuid,
    channel_id uuid,
    root_id timeuuid,
    team_id uuid,
    PRIMARY KEY (user_id, message_id)
) WITH CLUSTERING ORDER BY (message_id DESC);
