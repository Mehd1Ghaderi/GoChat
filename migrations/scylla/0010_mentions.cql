CREATE TABLE IF NOT EXISTS unread_mentions (
    user_id uuid,
    channel_id uuid,
    message_id timeuuid,
    PRIMARY KEY (user_id, channel_id, message_id)
) WITH default_time_to_live = 2592000;
