package engine

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// intervalValue is a calendar interval. Months and days stay separate from the
// time component so month/year arithmetic follows calendar rules instead of
// assuming a fixed month length.
type intervalValue struct {
	months int
	days   int
	nanos  int64
}

func (iv intervalValue) String() string {
	var parts []string
	if iv.months != 0 {
		parts = append(parts, fmt.Sprintf("%d mons", iv.months))
	}
	if iv.days != 0 {
		parts = append(parts, fmt.Sprintf("%d days", iv.days))
	}
	if iv.nanos != 0 || len(parts) == 0 {
		neg := iv.nanos < 0
		n := iv.nanos
		if neg {
			n = -n
		}
		h := n / int64(time.Hour)
		n %= int64(time.Hour)
		m := n / int64(time.Minute)
		n %= int64(time.Minute)
		s := float64(n) / float64(time.Second)
		prefix := ""
		if neg {
			prefix = "-"
		}
		parts = append(parts, fmt.Sprintf("%s%02d:%02d:%02g", prefix, h, m, s))
	}
	return strings.Join(parts, " ")
}

func (iv intervalValue) neg() intervalValue {
	return intervalValue{months: -iv.months, days: -iv.days, nanos: -iv.nanos}
}

func (iv intervalValue) add(o intervalValue) intervalValue {
	return intervalValue{months: iv.months + o.months, days: iv.days + o.days, nanos: iv.nanos + o.nanos}
}

func (iv intervalValue) scale(f float64) (intervalValue, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return intervalValue{}, errf("type_error", "interval scale must be finite")
	}
	const dayNanos = int64(24 * time.Hour)
	totalMonths := float64(iv.months) * f
	months := int(math.Trunc(totalMonths))
	fracMonths := totalMonths - float64(months)
	totalDays := float64(iv.days)*f + fracMonths*30
	days := int(math.Trunc(totalDays))
	fracDays := totalDays - float64(days)
	nanos := int64(math.Round(float64(iv.nanos)*f + fracDays*float64(dayNanos)))
	// Carry fractional days into the time component and whole days back out.
	days += int(nanos / dayNanos)
	nanos %= dayNanos
	return intervalValue{months: months, days: days, nanos: nanos}, nil
}

func (iv intervalValue) compare(o intervalValue) int {
	// Approximate calendar months where an exact comparison is not defined.
	am := int64(iv.months)*30*24*int64(time.Hour) + int64(iv.days)*24*int64(time.Hour) + iv.nanos
	bm := int64(o.months)*30*24*int64(time.Hour) + int64(o.days)*24*int64(time.Hour) + o.nanos
	if am < bm {
		return -1
	}
	if am > bm {
		return 1
	}
	return 0
}

func addInterval(t time.Time, iv intervalValue) time.Time {
	t = addMonthsClamped(t, iv.months)
	t = t.AddDate(0, 0, iv.days)
	return t.Add(time.Duration(iv.nanos))
}

func addTimeOnlyInterval(v string, iv intervalValue) (string, bool) {
	layouts := []string{"15:04:05.999999999", "15:04:05"}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, v); err == nil {
			t = t.AddDate(0, 0, iv.days)
			t = t.Add(time.Duration(iv.nanos))
			return t.Format("15:04:05"), true
		}
	}
	return "", false
}

func addMonthsClamped(t time.Time, months int) time.Time {
	if months == 0 {
		return t
	}
	total := int(t.Month()) - 1 + months
	year := t.Year() + total/12
	month := time.Month(total%12 + 1)
	if total%12 < 0 {
		year--
		month = 12
	}
	day := t.Day()
	last := time.Date(year, month+1, 0, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location()).Day()
	if day > last {
		day = last
	}
	return time.Date(year, month, day, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

func intervalBetween(start, end time.Time) intervalValue {
	d := end.Sub(start)
	days := int(d / (24 * time.Hour))
	return intervalValue{days: days, nanos: int64(d % (24 * time.Hour))}
}

var intervalWordRE = regexp.MustCompile(`(?i)([+-]?\d+(?:\.\d+)?)\s*(years?|yrs?|months?|mons?|weeks?|days?|hours?|hrs?|minutes?|mins?|seconds?|secs?|milliseconds?|microseconds?)`)
var intervalDayTimeRE = regexp.MustCompile(`^\s*([+-]?\d+)\s+(?:days?\s+)?([+-])?(\d+):(\d{1,2})(?::(\d{1,2}(?:\.\d+)?))?\s*$`)
var intervalTimeRE = regexp.MustCompile(`^\s*([+-])?(\d+):(\d{1,2})(?::(\d{1,2}(?:\.\d+)?))?\s*$`)
var intervalISORE = regexp.MustCompile(`(?i)^\s*([+-])?P(?:(\d+(?:\.\d+)?)Y)?(?:(\d+(?:\.\d+)?)M)?(?:(\d+(?:\.\d+)?)W)?(?:(\d+(?:\.\d+)?)D)?(?:T(?:(\d+(?:\.\d+)?)H)?(?:(\d+(?:\.\d+)?)M)?(?:(\d+(?:\.\d+)?)S)?)?\s*$`)

func parseInterval(value, qualifier string) (intervalValue, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return intervalValue{}, errf("type_error", "INTERVAL value is empty")
	}
	q := strings.ToUpper(strings.TrimSpace(qualifier))
	if q != "" {
		return parseQualifiedInterval(raw, q)
	}
	if m := intervalDayTimeRE.FindStringSubmatch(raw); m != nil {
		days, err := strconv.Atoi(m[1])
		if err != nil {
			return intervalValue{}, errf("type_error", "invalid INTERVAL day component")
		}
		nanos, err := parseIntervalTime(m[2], m[3], m[4], m[5])
		if err != nil {
			return intervalValue{}, err
		}
		return intervalValue{days: days, nanos: nanos}, nil
	}
	if m := intervalTimeRE.FindStringSubmatch(raw); m != nil {
		nanos, err := parseIntervalTime(m[1], m[2], m[3], m[4])
		if err != nil {
			return intervalValue{}, err
		}
		return intervalValue{nanos: nanos}, nil
	}
	if m := intervalISORE.FindStringSubmatch(raw); m != nil {
		iv := intervalValue{}
		mult := 1.0
		if m[1] == "-" {
			mult = -1
		}
		add := func(idx int, unit intervalValue) error {
			if m[idx] == "" {
				return nil
			}
			f, err := strconv.ParseFloat(m[idx], 64)
			if err != nil {
				return errf("type_error", "invalid ISO INTERVAL component")
			}
			scaled, err := unit.scale(f * mult)
			if err != nil {
				return err
			}
			iv = iv.add(scaled)
			return nil
		}
		if err := add(2, intervalValue{months: 12}); err != nil {
			return intervalValue{}, err
		}
		if err := add(3, intervalValue{months: 1}); err != nil {
			return intervalValue{}, err
		}
		if err := add(4, intervalValue{days: 7}); err != nil {
			return intervalValue{}, err
		}
		if err := add(5, intervalValue{days: 1}); err != nil {
			return intervalValue{}, err
		}
		if err := add(6, intervalValue{nanos: int64(time.Hour)}); err != nil {
			return intervalValue{}, err
		}
		if err := add(7, intervalValue{nanos: int64(time.Minute)}); err != nil {
			return intervalValue{}, err
		}
		if err := add(8, intervalValue{nanos: int64(time.Second)}); err != nil {
			return intervalValue{}, err
		}
		return iv, nil
	}
	matches := intervalWordRE.FindAllStringSubmatchIndex(raw, -1)
	if len(matches) > 0 {
		iv := intervalValue{}
		last := 0
		for _, m := range matches {
			if strings.TrimSpace(raw[last:m[0]]) != "" {
				return intervalValue{}, errf("type_error", "invalid INTERVAL value %q", value)
			}
			num, err := strconv.ParseFloat(raw[m[2]:m[3]], 64)
			if err != nil {
				return intervalValue{}, errf("type_error", "invalid INTERVAL number")
			}
			unit := strings.ToLower(raw[m[4]:m[5]])
			scaled, err := intervalUnitValue(unit, num)
			if err != nil {
				return intervalValue{}, err
			}
			iv = iv.add(scaled)
			last = m[1]
		}
		if strings.TrimSpace(raw[last:]) != "" {
			return intervalValue{}, errf("type_error", "invalid INTERVAL value %q", value)
		}
		return iv, nil
	}
	return intervalValue{}, errf("type_error", "invalid INTERVAL value %q", value)
}

func parseQualifiedInterval(raw, qualifier string) (intervalValue, error) {
	if strings.Contains(qualifier, " TO ") {
		parts := strings.SplitN(qualifier, " TO ", 2)
		switch parts[0] + " TO " + parts[1] {
		case "YEAR TO MONTH":
			var sign int64 = 1
			if strings.HasPrefix(raw, "-") {
				sign = -1
				raw = strings.TrimPrefix(raw, "-")
			}
			p := strings.SplitN(raw, "-", 2)
			if len(p) != 2 {
				return intervalValue{}, errf("type_error", "invalid YEAR TO MONTH interval")
			}
			y, err1 := strconv.Atoi(p[0])
			m, err2 := strconv.Atoi(p[1])
			if err1 != nil || err2 != nil {
				return intervalValue{}, errf("type_error", "invalid YEAR TO MONTH interval")
			}
			return intervalValue{months: int(sign) * (y*12 + m)}, nil
		case "DAY TO HOUR", "DAY TO MINUTE", "DAY TO SECOND", "HOUR TO MINUTE", "HOUR TO SECOND", "MINUTE TO SECOND":
			return parseQualifiedTimeRange(raw, parts[0], parts[1])
		}
		return intervalValue{}, errf("type_error", "unsupported INTERVAL qualifier %q", qualifier)
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil {
		return intervalValue{}, errf("type_error", "invalid INTERVAL number %q", raw)
	}
	return intervalUnitValue(strings.ToLower(qualifier), f)
}

func parseQualifiedTimeRange(raw, start, end string) (intervalValue, error) {
	sign := 1.0
	if strings.HasPrefix(raw, "-") {
		sign = -1
		raw = strings.TrimPrefix(raw, "-")
	}
	var days int
	if start == "DAY" {
		fields := strings.Fields(raw)
		if len(fields) < 2 {
			return intervalValue{}, errf("type_error", "invalid DAY TO interval")
		}
		d, err := strconv.Atoi(fields[0])
		if err != nil || d < 0 {
			return intervalValue{}, errf("type_error", "invalid DAY TO interval")
		}
		days = d
		raw = fields[1]
		if !strings.Contains(raw, ":") {
			f, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return intervalValue{}, errf("type_error", "invalid DAY TO interval")
			}
			return intervalValue{days: days, nanos: int64(sign * f * float64(time.Hour))}, nil
		}
	}
	parts := strings.Split(raw, ":")
	if len(parts) < 2 {
		return intervalValue{}, errf("type_error", "invalid time interval %q", raw)
	}
	nums := make([]float64, len(parts))
	for i, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return intervalValue{}, errf("type_error", "invalid time interval %q", raw)
		}
		nums[i] = v
	}
	days += int(nums[0] / 24)
	hours := nums[0]
	minutes := 0.0
	seconds := 0.0
	if len(nums) > 1 {
		minutes = nums[1]
	}
	if len(nums) > 2 {
		seconds = nums[2]
	}
	switch start {
	case "HOUR":
		hours = nums[0]
	case "MINUTE":
		hours, minutes = 0, nums[0]
	case "DAY":
		hours = nums[0]
	}
	if end == "HOUR" {
		minutes, seconds = 0, 0
	} else if end == "MINUTE" {
		seconds = 0
	}
	nanos := int64((hours*3600+minutes*60+seconds)*float64(time.Second)) * int64(sign)
	return intervalValue{days: days, nanos: nanos}, nil
}

func parseIntervalTime(sign, hour, minute, second string) (int64, error) {
	h, err := strconv.Atoi(hour)
	if err != nil {
		return 0, errf("type_error", "invalid INTERVAL time")
	}
	m, err := strconv.Atoi(minute)
	if err != nil {
		return 0, errf("type_error", "invalid INTERVAL time")
	}
	s := 0.0
	if second != "" {
		s, err = strconv.ParseFloat(second, 64)
		if err != nil {
			return 0, errf("type_error", "invalid INTERVAL time")
		}
	}
	n := int64((float64(h*3600+m*60) + s) * float64(time.Second))
	if sign == "-" {
		n = -n
	}
	return n, nil
}

func intervalUnitValue(unit string, n float64) (intervalValue, error) {
	switch strings.ToLower(unit) {
	case "year", "years", "yr", "yrs":
		return intervalValue{months: int(math.Round(n * 12))}, nil
	case "month", "months", "mon", "mons":
		return intervalValue{months: int(math.Round(n))}, nil
	case "week", "weeks":
		return intervalValue{days: int(math.Round(n * 7))}, nil
	case "day", "days":
		return intervalValue{days: int(math.Round(n))}, nil
	case "hour", "hours", "hr", "hrs":
		return intervalValue{nanos: int64(math.Round(n * float64(time.Hour)))}, nil
	case "minute", "minutes", "min", "mins":
		return intervalValue{nanos: int64(math.Round(n * float64(time.Minute)))}, nil
	case "second", "seconds", "sec", "secs":
		return intervalValue{nanos: int64(math.Round(n * float64(time.Second)))}, nil
	case "millisecond", "milliseconds":
		return intervalValue{nanos: int64(math.Round(n * float64(time.Millisecond)))}, nil
	case "microsecond", "microseconds":
		return intervalValue{nanos: int64(math.Round(n * float64(time.Microsecond)))}, nil
	default:
		return intervalValue{}, errf("type_error", "unsupported INTERVAL unit %q", unit)
	}
}
