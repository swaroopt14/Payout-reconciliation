"""Airflow Asset definitions for Clearline recon signals.

Event-first: signal_recon_dag schedules on AssetAny of these three.
Timer DAGs remain as backup and do not publish these assets.
"""

from __future__ import annotations

from .topics import (
    ASSET_URI_BANK_FILE,
    ASSET_URI_ERP_COMMIT,
    ASSET_URI_REFUND,
    SIGNAL_ASSET_URIS,
    SIGNAL_BANK_FILE,
    SIGNAL_ERP_COMMIT,
    SIGNAL_REFUND,
)

try:
    from airflow.sdk import Asset, AssetAny
except ImportError:  # pragma: no cover — unit tests import topics only
    Asset = None  # type: ignore
    AssetAny = None  # type: ignore


def build_signal_assets():
    """Return (erp, refund, bank) Asset objects, or None if Airflow absent."""
    if Asset is None:
        return None, None, None
    return (
        Asset(ASSET_URI_ERP_COMMIT),
        Asset(ASSET_URI_REFUND),
        Asset(ASSET_URI_BANK_FILE),
    )


def signal_schedule():
    """OR-schedule: any of the three signals wakes the DAG."""
    erp, refund, bank = build_signal_assets()
    if AssetAny is None or erp is None:
        return None
    return AssetAny(erp, refund, bank)


def asset_uri_for_signal(signal: str) -> str:
    return SIGNAL_ASSET_URIS[signal]


SIGNAL_NAMES = (SIGNAL_ERP_COMMIT, SIGNAL_REFUND, SIGNAL_BANK_FILE)
