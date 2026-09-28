CREATE TABLE comments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    author_id INTEGER NOT NULL REFERENCES users(id),
    body TEXT NOT NULL,
    created_at TEXT NOT NULL,
    client_id TEXT NOT NULL,
    UNIQUE(item_id, author_id, client_id)
);
CREATE INDEX comments_item ON comments(item_id, id);
