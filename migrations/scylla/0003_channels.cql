CREATE TABLE IF NOT EXISTS channels (
    id uuid PRIMARY KEY,
    team_id uuid,
    name text,
    display_name text,
    purpose text,
    kind text,
    created_by uuid,
    created_at timestamp,
    last_message_id timeuuid
);

CREATE TABLE IF NOT EXISTS team_channels (
    team_id uuid,
    channel_id uuid,
    kind text,
    PRIMARY KEY (team_id, channel_id)
);

CREATE TABLE IF NOT EXISTS channel_names (
    team_id uuid,
    name text,
    channel_id uuid,
    PRIMARY KEY ((team_id, name))
);

CREATE TABLE IF NOT EXISTS direct_channels (
    pair text PRIMARY KEY,
    channel_id uuid
);

CREATE TABLE IF NOT EXISTS channel_members (
    channel_id uuid,
    user_id uuid,
    team_id uuid,
    role text,
    joined_at timestamp,
    PRIMARY KEY (channel_id, user_id)
);

CREATE TABLE IF NOT EXISTS user_channels (
    user_id uuid,
    team_id uuid,
    channel_id uuid,
    role text,
    joined_at timestamp,
    PRIMARY KEY (user_id, team_id, channel_id)
);

CREATE TABLE IF NOT EXISTS channel_reads (
    user_id uuid,
    channel_id uuid,
    last_read_id timeuuid,
    PRIMARY KEY (user_id, channel_id)
);
