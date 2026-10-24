CREATE TABLE IF NOT EXISTS user_threads (
    user_id uuid,
    channel_id uuid,
    root_id timeuuid,
    team_id uuid,
    last_reply_at timestamp,
    read_at timestamp,
    PRIMARY KEY (user_id, channel_id, root_id)
) WITH default_time_to_live = 7776000;
