CREATE TABLE IF NOT EXISTS search_terms (
    scope uuid,
    term text,
    month int,
    message_id timeuuid,
    channel_id uuid,
    root_id timeuuid,
    user_id uuid,
    PRIMARY KEY ((scope, term, month), message_id)
) WITH CLUSTERING ORDER BY (message_id DESC);
