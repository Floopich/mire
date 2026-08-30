"""BQM storage schema migrations."""

from app.storage.migrations import Migration, add_column_if_missing, table_columns, table_exists


_SCHEMA = (
    """
    CREATE TABLE IF NOT EXISTS bqm_graphs (
        id INTEGER PRIMARY KEY AUTOINCREMENT, date TEXT NOT NULL UNIQUE,
        timestamp TEXT NOT NULL, image_blob BLOB NOT NULL
    )
    """,
    """
    CREATE TABLE IF NOT EXISTS bqm_data (
        id INTEGER PRIMARY KEY AUTOINCREMENT, timestamp TEXT NOT NULL,
        date TEXT NOT NULL, sent_polls INTEGER NOT NULL,
        lost_polls INTEGER NOT NULL DEFAULT 0, latency_min_ms REAL NOT NULL,
        latency_avg_ms REAL NOT NULL, latency_max_ms REAL NOT NULL,
        score INTEGER NOT NULL, UNIQUE(timestamp)
    )
    """,
    "CREATE INDEX IF NOT EXISTS idx_bqm_data_date ON bqm_data(date)",
    "CREATE TABLE IF NOT EXISTS bqm_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)",
)


def _apply_baseline(conn):
    for statement in _SCHEMA:
        conn.execute(statement)


def _baseline_applied(_conn):
    # Replay idempotent schema declarations once to repair missing indexes.
    return False


MIGRATIONS = (
    Migration("bqm-0001-baseline", _apply_baseline, _baseline_applied),
)
