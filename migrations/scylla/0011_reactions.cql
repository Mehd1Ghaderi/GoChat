CREATE TABLE IF NOT EXISTS reactions (
    channel_id uuid,
    bucket int,
    message_id timeuuid,
    emoji text,
    user_id uuid,
    PRIMARY KEY ((channel_id, bucket), message_id, emoji, user_id)
);
