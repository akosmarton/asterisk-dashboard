package service

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	yearRegex   = regexp.MustCompile(`(\d+)\s+year`)
	weekRegex   = regexp.MustCompile(`(\d+)\s+week`)
	dayRegex    = regexp.MustCompile(`(\d+)\s+day`)
	hourRegex   = regexp.MustCompile(`(\d+)\s+hour`)
	minuteRegex = regexp.MustCompile(`(\d+)\s+minute`)
	secondRegex = regexp.MustCompile(`(\d+)\s+second`)
)

// FormatShortUptime converts verbose Asterisk uptime string into compact format.
// Examples:
// "2 hours, 39 minutes, 34 seconds" -> "2h 39m 34s"
// "1 day, 4 hours, 12 minutes, 5 seconds" -> "1d 4h 12m"
// "3 weeks, 2 days, 1 hour" -> "3w 2d 1h"
func FormatShortUptime(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "N/A" {
		return "N/A"
	}

	extract := func(re *regexp.Regexp) (int, bool) {
		m := re.FindStringSubmatch(raw)
		if len(m) > 1 {
			val, err := strconv.Atoi(m[1])
			if err == nil {
				return val, true
			}
		}
		return 0, false
	}

	var parts []string
	var hasDaysOrMore bool

	if y, ok := extract(yearRegex); ok && y > 0 {
		parts = append(parts, fmt.Sprintf("%dy", y))
		hasDaysOrMore = true
	}
	if w, ok := extract(weekRegex); ok && w > 0 {
		parts = append(parts, fmt.Sprintf("%dw", w))
		hasDaysOrMore = true
	}
	if d, ok := extract(dayRegex); ok && d > 0 {
		parts = append(parts, fmt.Sprintf("%dd", d))
		hasDaysOrMore = true
	}
	if h, ok := extract(hourRegex); ok && h > 0 {
		parts = append(parts, fmt.Sprintf("%dh", h))
	}
	if m, ok := extract(minuteRegex); ok && m > 0 {
		parts = append(parts, fmt.Sprintf("%dm", m))
	}
	if s, ok := extract(secondRegex); ok {
		// Only omit seconds if we already have days/weeks/years to keep it clean and short
		if !hasDaysOrMore && len(parts) < 3 {
			parts = append(parts, fmt.Sprintf("%ds", s))
		}
	}

	if len(parts) == 0 {
		// Fallback: replace common words
		res := raw
		res = strings.ReplaceAll(res, " years", "y")
		res = strings.ReplaceAll(res, " year", "y")
		res = strings.ReplaceAll(res, " weeks", "w")
		res = strings.ReplaceAll(res, " week", "w")
		res = strings.ReplaceAll(res, " days", "d")
		res = strings.ReplaceAll(res, " day", "d")
		res = strings.ReplaceAll(res, " hours", "h")
		res = strings.ReplaceAll(res, " hour", "h")
		res = strings.ReplaceAll(res, " minutes", "m")
		res = strings.ReplaceAll(res, " minute", "m")
		res = strings.ReplaceAll(res, " seconds", "s")
		res = strings.ReplaceAll(res, " second", "s")
		res = strings.ReplaceAll(res, ",", "")
		return strings.TrimSpace(res)
	}

	return strings.Join(parts, " ")
}
