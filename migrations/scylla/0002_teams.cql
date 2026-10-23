CREATE TABLE IF NOT EXISTS teams (
    id uuid PRIMARY KEY,
    name text,
    display_name text,
    description text,
    created_by uuid,
    created_at timestamp
);

CREATE TABLE IF NOT EXISTS team_names (
    name text PRIMARY KEY,
    team_id uuid
);

CREATE TABLE IF NOT EXISTS team_members (
    team_id uuid,
    user_id uuid,
    role text,
    username text,
    display_name text,
    joined_at timestamp,
    PRIMARY KEY (team_id, user_id)
);

CREATE TABLE IF NOT EXISTS user_teams (
    user_id uuid,
    team_id uuid,
    role text,
    joined_at timestamp,
    PRIMARY KEY (user_id, team_id)
);

CREATE TABLE IF NOT EXISTS invites (
    token_hash blob PRIMARY KEY,
    team_id uuid,
    created_by uuid,
    created_at timestamp,
    expires_at timestamp,
    used_by uuid,
    used_at timestamp
);
