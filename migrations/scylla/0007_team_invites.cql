CREATE TABLE IF NOT EXISTS team_invites (
    team_id uuid,
    token_hash blob,
    created_by uuid,
    created_at timestamp,
    expires_at timestamp,
    PRIMARY KEY (team_id, token_hash)
);
