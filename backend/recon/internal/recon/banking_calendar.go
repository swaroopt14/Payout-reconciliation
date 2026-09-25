package recon

import (
	"time"
	_ "time/tzdata" // embed tz database so Asia/Kolkata resolves on minimal images
)

// istLocation is the civil-date zone for Indian banking days. Falls back to a
// fixed +05:30 zone if the tz database is somehow unavailable.
var istLocation = func() *time.Location {
	if loc, err := time.LoadLocation("Asia/Kolkata"); err == nil {
		return loc
	}
	return time.FixedZone("IST", 5*3600+1800)
}()

// ISTLocation returns the Asia/Kolkata location used by DefaultBankingCalendar.
func ISTLocation() *time.Location { return istLocation }

// BankingCalendar is a pure calendar helper: weekends plus a reference/injected
// holiday set. It never stores cash amounts — only whether a civil date is a
// banking day and how to roll forward to the next one.
type BankingCalendar struct {
	// Holidays are YYYY-MM-DD (civil date) keys. Values are optional names for
	// reference only; presence of the key makes the day non-banking.
	Holidays map[string]string
	Location *time.Location
}

// DefaultBankingCalendar returns weekends + ReferenceIndiaHolidays for nearby
// years, evaluated on IST (Asia/Kolkata) civil dates: an instant at
// 00:00-05:30 IST belongs to the IST day, not the prior UTC day.
func DefaultBankingCalendar() BankingCalendar {
	return BankingCalendar{
		Holidays: ReferenceIndiaHolidays(2025, 2026, 2027),
		Location: istLocation,
	}
}

// ReferenceIndiaHolidays is an in-code reference set of fixed national banking
// holidays (Republic Day, Independence Day, Gandhi Jayanti). It is not a full
// RBI calendar and carries no money columns. Callers may inject additional
// dates via BankingCalendar.Holidays.
func ReferenceIndiaHolidays(years ...int) map[string]string {
	out := map[string]string{}
	for _, y := range years {
		out[time.Date(y, 1, 26, 0, 0, 0, 0, time.UTC).Format("2006-01-02")] = "Republic Day"
		out[time.Date(y, 8, 15, 0, 0, 0, 0, time.UTC).Format("2006-01-02")] = "Independence Day"
		out[time.Date(y, 10, 2, 0, 0, 0, 0, time.UTC).Format("2006-01-02")] = "Gandhi Jayanti"
	}
	return out
}

func (c BankingCalendar) loc() *time.Location {
	if c.Location != nil {
		return c.Location
	}
	return time.UTC
}

// CivilDate truncates to midnight in the calendar location.
func (c BankingCalendar) CivilDate(t time.Time) time.Time {
	t = t.In(c.loc())
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, c.loc())
}

// IsBankingDay converts t to the calendar Location before taking the date.
// IsBankingDay is false on Saturday, Sunday, or a configured/reference holiday.
func (c BankingCalendar) IsBankingDay(t time.Time) bool {
	d := c.CivilDate(t)
	wd := d.Weekday()
	if wd == time.Saturday || wd == time.Sunday {
		return false
	}
	if len(c.Holidays) == 0 {
		return true
	}
	_, hol := c.Holidays[d.Format("2006-01-02")]
	return !hol
}

// NextBankingDay converts t to the calendar Location; the returned date is
// midnight in that Location (IST for DefaultBankingCalendar).
// NextBankingDay returns t's civil date when it is already a banking day,
// otherwise the next later banking day (never earlier).
func (c BankingCalendar) NextBankingDay(t time.Time) time.Time {
	d := c.CivilDate(t)
	for i := 0; i < 366; i++ {
		if c.IsBankingDay(d) {
			return d
		}
		d = d.AddDate(0, 0, 1)
	}
	return d
}
