ALTER TABLE channels ADD voice_disabled boolean;

ALTER TABLE attachments ADD voice boolean;
ALTER TABLE attachments ADD duration_ms int;

INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'channel_admin', 'channel', 'update', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_admin', 'channel', 'update', '', '', '');
