ALTER TABLE sessions ADD last_seen timestamp;
ALTER TABLE sessions ADD last_ip text;
ALTER TABLE user_sessions ADD last_seen timestamp;
ALTER TABLE user_sessions ADD last_ip text;

ALTER TABLE attachments ADD counted boolean;

CREATE TABLE IF NOT EXISTS storage_usage (
    user_id uuid PRIMARY KEY,
    used bigint
);

CREATE TABLE IF NOT EXISTS storage_quotas (
    user_id uuid PRIMARY KEY,
    quota bigint
);

INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'system_admin', 'user', 'find', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'system_admin', 'user', 'deactivate', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'system_admin', 'session', 'manage_any', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'system_admin', 'storage', 'manage_any', '', '', '');
