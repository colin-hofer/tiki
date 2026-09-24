CREATE TABLE users (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    email TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    role TEXT NOT NULL CHECK(role IN ('admin','member','viewer')),
    removed_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id INTEGER NOT NULL REFERENCES users(id),
    hash TEXT NOT NULL UNIQUE,
    expires_at INTEGER NOT NULL
);
CREATE INDEX sessions_expiry ON sessions(expires_at);
CREATE INDEX sessions_user ON sessions(user_id);
CREATE TABLE items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    type TEXT NOT NULL CHECK(type IN ('bug','feature','task')),
    status TEXT NOT NULL CHECK(status IN ('backlog','todo','in_progress','code_review','blocked','complete','void')),
    priority REAL NOT NULL CHECK(priority BETWEEN -1.7976931348623157e308 AND 1.7976931348623157e308),
    title TEXT NOT NULL CHECK(length(trim(title)) BETWEEN 1 AND 300),
    description TEXT NOT NULL DEFAULT '',
    created_by INTEGER NOT NULL REFERENCES users(id),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    version INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX items_priority ON items(priority, id);
CREATE INDEX items_status_priority ON items(status, priority, id);
CREATE TABLE item_assignees (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    user_id INTEGER NOT NULL REFERENCES users(id),
    PRIMARY KEY(item_id, user_id)
);
CREATE INDEX assignees_user ON item_assignees(user_id, item_id);
CREATE TABLE tags (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE
);
CREATE TABLE item_tags (
    item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    tag_id INTEGER NOT NULL REFERENCES tags(id),
    PRIMARY KEY(item_id, tag_id)
);
CREATE INDEX tags_items ON item_tags(tag_id, item_id);
CREATE TABLE activity (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    item_id INTEGER REFERENCES items(id),
    actor_id INTEGER NOT NULL REFERENCES users(id),
    kind TEXT NOT NULL,
    created_at TEXT NOT NULL,
    data TEXT NOT NULL
);
CREATE INDEX activity_item ON activity(item_id, id);
CREATE TABLE ordering (id INTEGER PRIMARY KEY CHECK(id = 1), generation INTEGER NOT NULL);
INSERT INTO ordering VALUES(1, 1);

CREATE TABLE invites (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    hash TEXT NOT NULL UNIQUE,
    role TEXT NOT NULL CHECK (role IN ('admin', 'member', 'viewer')),
    created_by INTEGER NOT NULL REFERENCES users(id),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL
);

CREATE TABLE user_revision (id INTEGER PRIMARY KEY CHECK(id = 1), revision INTEGER NOT NULL);
INSERT INTO user_revision VALUES(1, 0);
CREATE TRIGGER users_insert_revision AFTER INSERT ON users BEGIN
    UPDATE user_revision SET revision = revision + 1 WHERE id = 1;
END;
CREATE TRIGGER users_update_revision AFTER UPDATE OF name,email,role,removed_at ON users BEGIN
    UPDATE user_revision SET revision = revision + 1 WHERE id = 1;
END;

PRAGMA user_version = 4;
