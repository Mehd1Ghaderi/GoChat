ALTER TABLE channels ADD archived_at timestamp;

INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'channel_admin', 'channel', 'update', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_admin', 'channel', 'update', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'channel_admin', 'channel', 'archive', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_admin', 'channel', 'archive', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'channel_admin', 'channel', 'make_private', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_admin', 'channel', 'make_private', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_admin', 'channel', 'make_public', '', '', '');
