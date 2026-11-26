package server

import (
	"fmt"
	"reflect"
	"sort"
	"time"

	"ideusage"
)

// The settings of the IDE data usage (SiteSettings.IDE*, LLD 20): how they are checked and turned into the numbers the meter uses.

const (
	maxIDETermMB  = 1024
	maxIDEWindow  = 600
	maxIDEGrace   = 600
	maxIDELimitMB = 1048576
	maxIDEReserve = 1024
	maxIDESmallKB = 16384
	minIDEWarnPct = 1
	maxIDEWarnPct = 99
)

// checkIDEUsage says what is wrong with the IDE data usage settings, if anything.
func (s SiteSettings) checkIDEUsage() error {
	in := func(v, lo, hi int) bool { return v == 0 || (v >= lo && v <= hi) }
	switch {
	case !in(s.IDETermMB, 1, maxIDETermMB):
		return fmt.Errorf("the terminal speed must be between 1 and %d MB per window (or empty)", maxIDETermMB)
	case !in(s.IDETermWindowSecs, 1, maxIDEWindow):
		return fmt.Errorf("the terminal speed window must be between 1 and %d seconds (or empty)", maxIDEWindow)
	case !in(s.IDETermGraceSecs, 1, maxIDEGrace):
		return fmt.Errorf("the seconds over the terminal cap before a run is ended must be between 1 and %d (or empty)", maxIDEGrace)
	case !in(s.IDEGuestMB, 1, maxIDELimitMB):
		return fmt.Errorf("the guest session limit must be between 1 and %d MB (or empty)", maxIDELimitMB)
	case !in(s.IDEUserMB, 1, maxIDELimitMB):
		return fmt.Errorf("the daily limit for signed-in users must be between 1 and %d MB (or empty)", maxIDELimitMB)
	case !in(s.IDEWarnPct, minIDEWarnPct, maxIDEWarnPct):
		return fmt.Errorf("the warning must be between %d and %d percent (or empty)", minIDEWarnPct, maxIDEWarnPct)
	case !in(s.IDEReserveMB, 1, maxIDEReserve):
		return fmt.Errorf("the reserve must be between 1 and %d MB (or empty)", maxIDEReserve)
	case !in(s.IDESmallKB, 1, maxIDESmallKB):
		return fmt.Errorf("the small file size at the limit must be between 1 and %d KB (or empty)", maxIDESmallKB)
	case !in(s.IDEHistoryDays, 30, 365):
		return fmt.Errorf("the days of history kept must be between 30 and 365 (or empty)")
	case !in(s.IDEFlagLimitDays, 1, 30):
		return fmt.Errorf("the days in a row at the limit must be between 1 and 30 (or empty)")
	case !in(s.IDEFlagJump, 2, 50):
		return fmt.Errorf("the jump over the usual day must be between 2 and 50 times (or empty)")
	case !in(s.IDEFlagNearPct, 50, 99):
		return fmt.Errorf("the share of the limit to flag must be between 50 and 99 percent (or empty)")
	case !in(s.IDEFlagStops, 1, 1000):
		return fmt.Errorf("the number of times a terminal was stopped must be between 1 and 1000 (or empty)")
	case s.IDEResetMinutes < 0 || s.IDEResetMinutes > 1439:
		return fmt.Errorf("the reset time must be a time of day (0 to 1439 minutes after 00:00 UTC)")
	}
	for _, p := range s.IDEPartsOff {
		if _, ok := ideusage.ParsePart(p); !ok {
			return fmt.Errorf("%q is not a part of the IDE", p)
		}
	}
	return nil
}

// cleanIDEParts is the parts that are switched off, once each and in order.
func cleanIDEParts(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range in {
		if _, ok := ideusage.ParsePart(p); ok && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// ideSettingsDiffer reports whether any IDE data usage setting changed.
func ideSettingsDiffer(a, b SiteSettings) bool {
	return a.IDEUsageOff != b.IDEUsageOff || a.IDEAdminsCounted != b.IDEAdminsCounted ||
		!reflect.DeepEqual(cleanIDEParts(a.IDEPartsOff), cleanIDEParts(b.IDEPartsOff)) ||
		a.IDETermMB != b.IDETermMB || a.IDETermWindowSecs != b.IDETermWindowSecs || a.IDETermGraceSecs != b.IDETermGraceSecs ||
		a.IDETermPauseOnly != b.IDETermPauseOnly || a.IDEGuestMB != b.IDEGuestMB || a.IDEUserMB != b.IDEUserMB ||
		a.IDEWarnPct != b.IDEWarnPct || a.IDEReserveMB != b.IDEReserveMB || a.IDESmallKB != b.IDESmallKB ||
		a.IDEResetMinutes != b.IDEResetMinutes || a.IDEHistoryDays != b.IDEHistoryDays || a.IDEFlagLimitDays != b.IDEFlagLimitDays ||
		a.IDEFlagJump != b.IDEFlagJump || a.IDEFlagNearPct != b.IDEFlagNearPct || a.IDEFlagStops != b.IDEFlagStops || a.IDEFlagLog != b.IDEFlagLog
}

// ideConfig is the settings with the built-in value where one is empty.
func (s SiteSettings) ideConfig() ideusage.Config {
	c := ideusage.Defaults()
	c.On = !s.IDEUsageOff
	c.AdminsExempt = !s.IDEAdminsCounted
	for _, p := range s.IDEPartsOff {
		if part, ok := ideusage.ParsePart(p); ok {
			c.Counted[part] = false
		}
	}
	if s.IDETermMB > 0 {
		c.TermBytes = int64(s.IDETermMB) * ideusage.MB
	}
	if s.IDETermWindowSecs > 0 {
		c.TermWindow = time.Duration(s.IDETermWindowSecs) * time.Second
	}
	if s.IDETermGraceSecs > 0 {
		c.TermGrace = time.Duration(s.IDETermGraceSecs) * time.Second
	}
	c.TermPauseOnly = s.IDETermPauseOnly
	if s.IDEGuestMB > 0 {
		c.GuestBytes = int64(s.IDEGuestMB) * ideusage.MB
	}
	if s.IDEUserMB > 0 {
		c.UserBytes = int64(s.IDEUserMB) * ideusage.MB
	}
	if s.IDEWarnPct > 0 {
		c.WarnPct = s.IDEWarnPct
	}
	if s.IDEReserveMB > 0 {
		c.ReserveBytes = int64(s.IDEReserveMB) * ideusage.MB
	}
	if s.IDESmallKB > 0 {
		c.SmallBytes = int64(s.IDESmallKB) << 10
	}
	c.ResetMinutes = s.IDEResetMinutes
	if s.IDEHistoryDays > 0 {
		c.HistoryDays = s.IDEHistoryDays
	}
	if s.IDEFlagLimitDays > 0 {
		c.FlagLimitDays = s.IDEFlagLimitDays
	}
	if s.IDEFlagJump > 0 {
		c.FlagJump = s.IDEFlagJump
	}
	if s.IDEFlagNearPct > 0 {
		c.FlagNearPct = s.IDEFlagNearPct
	}
	if s.IDEFlagStops > 0 {
		c.FlagStops = s.IDEFlagStops
	}
	c.FlagLog = s.IDEFlagLog
	return c
}
