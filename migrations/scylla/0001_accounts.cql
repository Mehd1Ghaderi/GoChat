CREATE TABLE IF NOT EXISTS users (
    id uuid PRIMARY KEY,
    username text,
    email text,
    display_name text,
    password_hash text,
    system_role text,
    totp_enabled boolean,
    totp_secret blob,
    totp_pending blob,
    totp_last_step bigint,
    deactivated boolean,
    created_at timestamp,
    updated_at timestamp
);

CREATE TABLE IF NOT EXISTS usernames (
    username text PRIMARY KEY,
    user_id uuid
);

CREATE TABLE IF NOT EXISTS emails (
    email text PRIMARY KEY,
    user_id uuid
);

CREATE TABLE IF NOT EXISTS system_state (
    key text PRIMARY KEY,
    value text
);

CREATE TABLE IF NOT EXISTS recovery_codes (
    user_id uuid,
    code_hash blob,
    PRIMARY KEY (user_id, code_hash)
);

CREATE TABLE IF NOT EXISTS sessions (
    token_hash blob PRIMARY KEY,
    user_id uuid,
    created_at timestamp,
    expires_at timestamp,
    ip text,
    user_agent text
);

CREATE TABLE IF NOT EXISTS user_sessions (
    user_id uuid,
    token_hash blob,
    created_at timestamp,
    expires_at timestamp,
    ip text,
    user_agent text,
    PRIMARY KEY (user_id, token_hash)
);

CREATE TABLE IF NOT EXISTS mfa_challenges (
    token_hash blob PRIMARY KEY,
    user_id uuid,
    attempts int,
    expires_at timestamp,
    ip text,
    user_agent text
);
