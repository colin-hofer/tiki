-- Version 4: soft removal and live updates to the people directory.
ALTER TABLE users ADD COLUMN removed_at INTEGER NOT NULL DEFAULT 0;
CREATE TABLE user_revision (id INTEGER PRIMARY KEY CHECK(id = 1), revision INTEGER NOT NULL);
INSERT INTO user_revision VALUES(1, 0);
CREATE TRIGGER users_insert_revision AFTER INSERT ON users BEGIN
    UPDATE user_revision SET revision = revision + 1 WHERE id = 1;
END;
CREATE TRIGGER users_update_revision AFTER UPDATE OF name,email,role,removed_at ON users BEGIN
    UPDATE user_revision SET revision = revision + 1 WHERE id = 1;
END;
