CREATE TABLE IF NOT EXISTS channel_prefs (
    user_id uuid,
    channel_id uuid,
    muted boolean,
    PRIMARY KEY (user_id, channel_id)
);
