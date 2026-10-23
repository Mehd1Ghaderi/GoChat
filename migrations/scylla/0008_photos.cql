ALTER TABLE users ADD photo text;

ALTER TABLE teams ADD photo text;

INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_admin', 'team', 'update', '', '', '');
