"""Journal storage schema migrations."""

from app.storage.migrations import (
    Migration,
    add_column_if_missing,
    table_columns,
    table_exists,
)


_SCHEMA = (
    """
    CREATE TABLE IF NOT EXISTS journal_entries (
        id INTEGER PRIMARY KEY AUTOINCREMENT, date TEXT NOT NULL, title TEXT NOT NULL,
        description TEXT, icon TEXT, incident_id INTEGER, created_at TEXT NOT NULL,
        updated_at TEXT NOT NULL
    )
    """,
    """
    CREATE TABLE IF NOT EXISTS journal_attachments (
        id INTEGER PRIMARY KEY AUTOINCREMENT, entry_id INTEGER NOT NULL,
        filename TEXT NOT NULL, mime_type TEXT NOT NULL, data BLOB NOT NULL,
        created_at TEXT NOT NULL,
        FOREIGN KEY (entry_id) REFERENCES journal_entries(id) ON DELETE CASCADE
    )
    """,
    """
    CREATE TABLE IF NOT EXISTS incidents (
        id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, description TEXT,
        status TEXT NOT NULL DEFAULT 'open', start_date TEXT, end_date TEXT, icon TEXT,
        created_at TEXT NOT NULL, updated_at TEXT NOT NULL
    )
    """,
)


def _apply_baseline(conn):
    for statement in _SCHEMA:
        conn.execute(statement)


def _baseline_applied(_conn):
    # Replay idempotent schema declarations once to repair missing indexes.
    return False


MIGRATIONS = (
    Migration("journal-0001-baseline", _apply_baseline, _baseline_applied),
)
