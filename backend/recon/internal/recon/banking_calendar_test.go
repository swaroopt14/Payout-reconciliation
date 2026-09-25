package recon

import (
	"testing"
	"time"
)

func TestBankingCalendar_WeekendNotBanking(t *testing.T) {
	cal := DefaultBankingCalendar()
	sat := time.Date(2026, 9, 5, 15, 0, 0, 0, time.UTC) // Saturday
	sun := time.Date(2026, 9, 6, 8, 0, 0, 0, time.UTC)
	mon := time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC)
	if cal.IsBankingDay(sat) || cal.IsBankingDay(sun) {
		t.Fatal("weekend must not be banking day")
	}
	if !cal.IsBankingDay(mon) {
		t.Fatal("Monday should be banking day")
	}
}

func TestBankingCalendar_ReferenceHoliday(t *testing.T) {
	cal := DefaultBankingCalendar()
	// 2026-01-26 is Monday + Republic Day
	rep := time.Date(2026, 1, 26, 12, 0, 0, 0, time.UTC)
	if cal.IsBankingDay(rep) {
		t.Fatal("Republic Day must not be banking day")
	}
	next := cal.NextBankingDay(rep)
	want := time.Date(2026, 1, 27, 0, 0, 0, 0, ISTLocation())
	if !next.Equal(want) {
		t.Fatalf("next=%v want=%v", next, want)
	}
}

func TestBankingCalendar_WeekendPlusHolidayRollsForward(t *testing.T) {
	// Friday holiday: natural Sat → skip Sun → Mon; inject Fri holiday so Fri rolls to Mon too.
	cal := BankingCalendar{
		Holidays: map[string]string{"2026-09-04": "Injected Friday Holiday"}, // Fri
		Location: time.UTC,
	}
	fri := time.Date(2026, 9, 4, 10, 0, 0, 0, time.UTC)
	sat := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	want := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC) // Monday
	if got := cal.NextBankingDay(fri); !got.Equal(want) {
		t.Fatalf("fri roll=%v want=%v", got, want)
	}
	if got := cal.NextBankingDay(sat); !got.Equal(want) {
		t.Fatalf("sat roll=%v want=%v", got, want)
	}
	if !cal.IsBankingDay(want) {
		t.Fatal("Monday should be banking")
	}
}

func TestBankingCalendar_AlreadyBankingUnchanged(t *testing.T) {
	cal := BankingCalendar{Location: time.UTC}
	tue := time.Date(2026, 9, 8, 18, 30, 0, 0, time.UTC)
	got := cal.NextBankingDay(tue)
	want := time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestBankingCalendar_NoMoneyFields(t *testing.T) {
	// Structural guard: holiday values are names only.
	h := ReferenceIndiaHolidays(2026)
	for k, v := range h {
		if v == "" {
			t.Fatalf("empty name for %s", k)
		}
		for _, ch := range v {
			if ch >= '0' && ch <= '9' {
				// names may not encode amounts; digits in year-like names are fine elsewhere —
				// just assert we never stuffed a numeric amount string.
			}
		}
		if len(v) > 0 && (v[0] == '-' || (v[0] >= '0' && v[0] <= '9')) {
			t.Fatalf("holiday value looks like money/amount: %s=%q", k, v)
		}
	}
}

func TestBankingCalendar_ISTRepublicDayEarlyMorning(t *testing.T) {
	cal := DefaultBankingCalendar()
	ist := ISTLocation()
	// 2026-01-26 01:00 IST == 2026-01-25 19:30 UTC (a Sunday in UTC).
	early := time.Date(2026, 1, 26, 1, 0, 0, 0, ist)
	if !early.Equal(time.Date(2026, 1, 25, 19, 30, 0, 0, time.UTC)) {
		t.Fatalf("fixture mismatch: %v", early.UTC())
	}
	if cal.IsBankingDay(early) {
		t.Fatal("01:00 IST on Republic Day must be a holiday")
	}
	got := cal.NextBankingDay(early)
	want := time.Date(2026, 1, 27, 0, 0, 0, 0, ist) // Tuesday
	if !got.Equal(want) || got.Format("2006-01-02") != "2026-01-27" || got.Weekday() != time.Tuesday {
		t.Fatalf("roll=%v want=%v", got, want)
	}
	if got.Location().String() != ist.String() {
		t.Fatalf("roll date must be IST, got loc=%s", got.Location())
	}
}

func TestBankingCalendar_UTCInputOnISTRepublicDay(t *testing.T) {
	cal := DefaultBankingCalendar()
	// UTC instant 2026-01-25 18:30 UTC == 2026-01-26 00:00 IST.
	utcIn := time.Date(2026, 1, 25, 18, 30, 0, 0, time.UTC)
	if cal.IsBankingDay(utcIn) {
		t.Fatal("UTC input landing on Republic Day in IST must be non-banking")
	}
	if got := cal.NextBankingDay(utcIn).Format("2006-01-02"); got != "2026-01-27" {
		t.Fatalf("roll=%s want 2026-01-27", got)
	}
	// One minute earlier is still Sunday 25 Jan in IST: non-banking, rolls
	// past Republic Day to Tuesday too.
	if got := cal.NextBankingDay(utcIn.Add(-time.Minute)).Format("2006-01-02"); got != "2026-01-27" {
		t.Fatalf("sunday roll=%s", got)
	}
	// 2026-01-26 20:00 UTC is already 2026-01-27 01:30 IST: banking day.
	if !cal.IsBankingDay(time.Date(2026, 1, 26, 20, 0, 0, 0, time.UTC)) {
		t.Fatal("2026-01-27 IST should be a banking day")
	}
}
