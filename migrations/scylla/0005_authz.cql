CREATE TABLE IF NOT EXISTS casbin_rules (
    ptype text,
    v0 text,
    v1 text,
    v2 text,
    v3 text,
    v4 text,
    v5 text,
    PRIMARY KEY (ptype, v0, v1, v2, v3, v4, v5)
);

INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('g', 'system_admin', 'system_user', '', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('g', 'team_admin', 'team_member', '', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('g', 'channel_admin', 'channel_member', '', '', '', '');

INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'system_admin', '*', '*', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'system_user', 'team', 'create', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'system_user', 'direct', 'create', '', '', '');

INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_member', 'team', 'read', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_member', 'channel', 'browse', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_member', 'channel', 'join', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_member', 'channel', 'create_public', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_member', 'channel', 'create_private', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_admin', 'invite', 'create', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_admin', 'team_member', 'remove', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_admin', 'team_member', 'update_role', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_admin', 'channel_member', 'remove', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'team_admin', 'message', 'delete_any', '', '', '');

INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'channel_member', 'message', 'read', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'channel_member', 'message', 'create', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'channel_member', 'message', 'edit_own', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'channel_member', 'message', 'delete_own', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'channel_member', 'file', 'upload', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'channel_member', 'channel_member', 'add', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'channel_admin', 'channel_member', 'remove', '', '', '');
INSERT INTO casbin_rules (ptype, v0, v1, v2, v3, v4, v5) VALUES ('p', 'channel_admin', 'message', 'delete_any', '', '', '');
