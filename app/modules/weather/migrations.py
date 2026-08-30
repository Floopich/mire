"""Weather storage schema migrations."""

from app.storage.migrations import Migration, add_column_if_missing, table_columns, table_exists


def _apply_baseline(conn):
    conn.execute(
        "CREATE TABLE IF NOT EXISTS weather_data ("
        "timestamp TEXT PRIMARY KEY, temperature REAL NOT NULL)"
    )


def _baseline_applied(_conn):
    # Replay idempotent schema declarations once to repair missing indexes.
    return False


MIGRATIONS = (
    Migration("weather-0001-baseline", _apply_baseline, _baseline_applied),
)
