CREATE TABLE github_user_tokens (
    actor_id   TEXT PRIMARY KEY REFERENCES actors (id) ON DELETE CASCADE,
    ciphertext BLOB NOT NULL,
    updated_at TEXT NOT NULL
);
