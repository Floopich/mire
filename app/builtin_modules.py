"""Static registry for Mire's built-in modules.

Built-ins are part of the application release.  Keep their directory list and
Python contribution symbols explicit so startup does not discover core modules
by scanning a plugin directory.
"""

from __future__ import annotations

from dataclasses import dataclass


BUILTIN_MODULE_DIRS: tuple[str, ...] = (
    "backup",
    "be_compensation",
    "comparison",
    "connection_monitor",
    "evidence",
    "journal",
    "modulation",
    "mqtt",
    "reports",
    "speedtest",
    "weather",
)


@dataclass(frozen=True)
class BuiltinPythonContributions:
    """Import paths for Python entry points owned by a built-in module."""

    collector: str | None = None
    publisher: str | None = None


BUILTIN_PYTHON_CONTRIBUTIONS: dict[str, BuiltinPythonContributions] = {
    "mire.backup": BuiltinPythonContributions(
        collector="app.modules.backup.collector:BackupCollector",
    ),
    "mire.connection_monitor": BuiltinPythonContributions(
        collector="app.modules.connection_monitor.collector:ConnectionMonitorCollector",
    ),
    "mire.mqtt": BuiltinPythonContributions(
        publisher="app.modules.mqtt.publisher:MQTTPublisher",
    ),
    "mire.speedtest": BuiltinPythonContributions(
        collector="app.modules.speedtest.collector:SpeedtestCollector",
    ),
    "mire.weather": BuiltinPythonContributions(
        collector="app.modules.weather.collector:WeatherCollector",
    ),
}
