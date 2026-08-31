package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// sinceFlag accepts a span written the way people actually write one: "30d",
// "2w", "12h", or a bare number meaning days. Go's flag.Duration only knows
// units up to "h", so "--since 30d" was a parse error and "--since 30" was
// too — while the help text advertised "--since D". Only "720h" worked.
type sinceFlag struct{ d *time.Duration }

func (f sinceFlag) String() string {
	if f.d == nil || *f.d == 0 {
		return ""
	}
	if h := f.d.Hours(); h >= 24 && h == float64(int64(h)) && int64(h)%24 == 0 {
		return strconv.FormatInt(int64(h)/24, 10) + "d"
	}
	return f.d.String()
}

func (f sinceFlag) Set(s string) error {
	d, err := parseSince(s)
	if err != nil {
		return err
	}
	*f.d = d
	return nil
}

// parseSince turns a human span into a duration. A bare number means days,
// which is what "--since 30" reads as to everyone who types it.
func parseSince(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, nil
	}
	unit := time.Hour * 24
	num := s
	switch {
	case strings.HasSuffix(s, "d"):
		num = strings.TrimSuffix(s, "d")
	case strings.HasSuffix(s, "w"):
		num, unit = strings.TrimSuffix(s, "w"), time.Hour*24*7
	default:
		// Anything with an explicit sub-day unit is a plain Go duration.
		if strings.ContainsAny(s, "hms") {
			d, err := time.ParseDuration(s)
			if err != nil {
				return 0, fmt.Errorf("%q is not a span: try 30d, 2w or 12h", s)
			}
			if d < 0 {
				return 0, fmt.Errorf("%q is negative", s)
			}
			return d, nil
		}
	}
	n, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a span: try 30d, 2w or 12h", s)
	}
	if n < 0 {
		return 0, fmt.Errorf("%q is negative", s)
	}
	return time.Duration(n * float64(unit)), nil
}
