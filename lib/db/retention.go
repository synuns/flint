package db

import (
	"time"

	"github.com/synuns/flint/lib/store"
)

const maxRetentionDays = 106751

const MaxRetentionDays = maxRetentionDays

func DaysCutoff(days int, now time.Time) time.Time {
	if days > maxRetentionDays {
		days = maxRetentionDays
	}
	if days < 0 {
		days = 0
	}
	return now.AddDate(0, 0, -days)
}

func DaysSpan(days int, now time.Time) time.Duration {
	return now.Sub(DaysCutoff(days, now))
}

func retentionCutoffDays(days, def int, now time.Time) time.Time {
	if days <= 0 {
		days = def
	}
	return DaysCutoff(days, now)
}

func defForRetention(def store.Thresholds, table string) int {
	switch table {
	case "hot_events":
		return def.HotRetentionD
	case "knowledge_reads":
		return def.KreadsRetentionD
	default:
		return def.EventsRetentionD
	}
}

const maxSpanHours = 87600

func HoursSpan(h, def int) time.Duration {
	if h <= 0 {
		h = def
	}
	if h > maxSpanHours {
		h = maxSpanHours
	}
	if h < 0 {
		h = 0
	}
	return time.Duration(h) * time.Hour
}

func MinutesSpan(m, def int) time.Duration {
	if m <= 0 {
		m = def
	}
	if max := maxSpanHours * 60; m > max {
		m = max
	}
	if m < 0 {
		m = 0
	}
	return time.Duration(m) * time.Minute
}

func gateStampDue(t, now time.Time) bool {
	if t.After(now) {
		return true
	}
	return now.Sub(t) >= 24*time.Hour
}
