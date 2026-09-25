"""Python banking-day gate mirroring Go BankingCalendar semantics.

Source of truth for holiday money roll-forward lives in
backend/recon/internal/recon/banking_calendar.go (DefaultBankingCalendar /
ReferenceIndiaHolidays / IsBankingDay / NextBankingDay).

This module only answers calendar questions for the Airflow gate. It does
NOT bucket cash amounts, set MATCHED, or treat projections as bank cash.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import date, datetime, timedelta, timezone
from typing import Dict, Iterable, Mapping, Optional, Union

DateLike = Union[date, datetime, str]


# Holiday source labels (same vocabulary Research uses):
#   rbi_list    - national holiday on the RBI (Negotiable Instruments Act) list
#   psp_overlay - extra non-settlement day announced by a PSP, not on the RBI list
SOURCE_RBI_LIST = "rbi_list"
SOURCE_PSP_OVERLAY = "psp_overlay"
HOLIDAY_SOURCES = (SOURCE_RBI_LIST, SOURCE_PSP_OVERLAY)

# (month, day, name, source). Must match Go ReferenceIndiaHolidays in
# backend/recon/internal/recon/banking_calendar.go; the parity test in
# tests/test_signal_scheduler.py reads the Go file and fails on any drift.
REFERENCE_INDIA_HOLIDAY_ROWS = (
    (1, 26, "Republic Day", SOURCE_RBI_LIST),
    (8, 15, "Independence Day", SOURCE_RBI_LIST),
    (10, 2, "Gandhi Jayanti", SOURCE_RBI_LIST),
)

# Years covered by default_banking_calendar (Go DefaultBankingCalendar).
DEFAULT_CALENDAR_YEARS = (2025, 2026, 2027)


def reference_india_holiday_rows(*years: int) -> list:
    """Dated rows with name + source label (rbi_list / psp_overlay)."""
    return [
        {"date": f"{y:04d}-{m:02d}-{d:02d}", "name": name, "source": source}
        for y in years
        for (m, d, name, source) in REFERENCE_INDIA_HOLIDAY_ROWS
    ]


def reference_india_holidays(*years: int) -> Dict[str, str]:
    """Fixed national banking holidays, same set as Go ReferenceIndiaHolidays.

    Returns YYYY-MM-DD to name. Not a full RBI calendar. Callers may inject
    more via BankingCalendar.holidays. Source labels: reference_india_holiday_rows.
    """
    return {r["date"]: r["name"] for r in reference_india_holiday_rows(*years)}


# Banking days are Indian civil dates, matching Go DefaultBankingCalendar
# (Location Asia/Kolkata). India has no DST, so a fixed +05:30 is exact.
CALENDAR_LOCATION = "Asia/Kolkata"
IST = timezone(timedelta(hours=5, minutes=30), "IST")


def _as_date(value: DateLike) -> date:
    """IST civil date for a date, datetime, or ISO string.

    Aware datetimes and ISO strings with an offset (Z, +00:00, +05:30) are
    converted to IST first, so 2026-01-25T19:30:00Z is 26 Jan. Naive values
    and bare YYYY-MM-DD are taken as IST civil dates already.
    """
    if isinstance(value, datetime):
        if value.tzinfo is not None:
            return value.astimezone(IST).date()
        return value.date()
    if isinstance(value, date):
        return value
    text = str(value).strip()
    if len(text) <= 10:
        return date.fromisoformat(text)
    return _as_date(datetime.fromisoformat(text.replace("Z", "+00:00")))


@dataclass
class BankingCalendar:
    """Weekends + optional holiday map (YYYY-MM-DD → name)."""

    holidays: Dict[str, str] = field(default_factory=dict)

    def civil_date(self, value: DateLike) -> date:
        return _as_date(value)

    def is_banking_day(self, value: DateLike) -> bool:
        d = self.civil_date(value)
        if d.weekday() >= 5:  # Saturday=5, Sunday=6
            return False
        if not self.holidays:
            return True
        return d.isoformat() not in self.holidays

    def next_banking_day(self, value: DateLike) -> date:
        """Same-or-next banking day (never earlier) — mirrors Go NextBankingDay."""
        d = self.civil_date(value)
        for _ in range(366):
            if self.is_banking_day(d):
                return d
            d = d + timedelta(days=1)
        return d


def default_banking_calendar() -> BankingCalendar:
    return BankingCalendar(holidays=reference_india_holidays(*DEFAULT_CALENDAR_YEARS))


def gate_banking_day(
    when: Optional[DateLike] = None,
    calendar: Optional[BankingCalendar] = None,
) -> dict:
    """Return gate decision for scheduler tasks.

    On a banking day: allow=True.
    Otherwise: allow=False, deferred_to=NextBankingDay (skip/defer semantics).
    """
    cal = calendar or default_banking_calendar()
    as_of = cal.civil_date(when or datetime.now(IST))
    if cal.is_banking_day(as_of):
        return {
            "allow": True,
            "as_of": as_of.isoformat(),
            "deferred_to": None,
            "reason": "banking_day",
        }
    nxt = cal.next_banking_day(as_of)
    return {
        "allow": False,
        "as_of": as_of.isoformat(),
        "deferred_to": nxt.isoformat(),
        "reason": "non_banking_day",
    }




def timer_gate(when: Optional[DateLike] = None, calendar: Optional[BankingCalendar] = None) -> dict:
    """Banking-day gate for BACKUP timer DAGs (same rule as signal_recon_dag).

    Aware datetimes are converted to IST before taking the civil date, so a
    UTC run at 20:00 on 25 Jan counts as 26 Jan (Republic Day) in India.
    """
    return gate_banking_day(when if when is not None else datetime.now(IST), calendar)


def timer_should_run(**context) -> bool:
    """ShortCircuitOperator callable: False on weekends / reference holidays.

    Returning False skips every downstream task of the timer run (deferred to
    the next scheduled run on a banking day). Pushes the gate to XCom.
    """
    when = context.get("logical_date") or context.get("data_interval_end")
    gate = timer_gate(when, context.get("banking_calendar"))
    ti = context.get("ti")
    if ti is not None:
        ti.xcom_push(key="banking_gate", value=gate)
    return bool(gate["allow"])
