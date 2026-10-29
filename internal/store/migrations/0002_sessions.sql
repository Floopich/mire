-- Sessions d'administration.
--
-- On ne stocke que l'empreinte SHA-256 du jeton, jamais le jeton lui-meme :
-- une copie de la base ne suffit pas a se connecter a la place de quelqu'un.
CREATE TABLE sessions (
    token_hash  TEXT    PRIMARY KEY,
    created_at  INTEGER NOT NULL,
    expires_at  INTEGER NOT NULL
);
CREATE INDEX idx_sessions_expires ON sessions(expires_at);
