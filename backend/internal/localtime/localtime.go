// Package localtime defines the platform's accounting calendar: budgets and
// "today" statistics use natural days and months in Asia/Shanghai (UTC+8,
// which has no daylight saving time).
package localtime

import "time"

// Zone is the accounting time zone.
var Zone = time.FixedZone("Asia/Shanghai", 8*60*60)

// DayStart is midnight of the accounting day containing t.
func DayStart(t time.Time) time.Time {
	local := t.In(Zone)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, Zone)
}

// MonthStart is midnight of the first day of the accounting month containing t.
func MonthStart(t time.Time) time.Time {
	local := t.In(Zone)
	return time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, Zone)
}
