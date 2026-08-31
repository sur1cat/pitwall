// Package ui renders terminal output: colors when a TTY is attached, plain
// text when piped, and never any escape codes when NO_COLOR is set.
package ui

import (
	"fmt"
	"os"
	"strings"
	"time"
)

var enabled = detectColor()

func detectColor() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// SetColor forces color output on or off.
func SetColor(on bool) { enabled = on }

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	blue   = "\033[34m"
	cyan   = "\033[36m"
	gray   = "\033[90m"
)

func paint(code, s string) string {
	if !enabled || s == "" {
		return s
	}
	return code + s + reset
}

// Bold renders emphasized text.
func Bold(s string) string { return paint(bold, s) }

// Dim renders de-emphasized text.
func Dim(s string) string { return paint(dim, s) }

// Gray renders secondary text.
func Gray(s string) string { return paint(gray, s) }

// Red renders an alarming value.
func Red(s string) string { return paint(red, s) }

// Green renders a healthy value.
func Green(s string) string { return paint(green, s) }

// Yellow renders a value that needs attention.
func Yellow(s string) string { return paint(yellow, s) }

// Blue renders an informational value.
func Blue(s string) string { return paint(blue, s) }

// Cyan renders a highlighted value.
func Cyan(s string) string { return paint(cyan, s) }

// Width returns the printable width of s, ignoring escape codes.
func Width(s string) int {
	n, inEscape := 0, false
	for _, r := range s {
		switch {
		case r == '\033':
			inEscape = true
		case inEscape:
			if r == 'm' {
				inEscape = false
			}
		default:
			n++
		}
	}
	return n
}

// Pad right-pads s to width, ignoring escape codes.
func Pad(s string, width int) string {
	if n := Width(s); n < width {
		return s + strings.Repeat(" ", width-n)
	}
	return s
}

// Truncate shortens s to width, appending an ellipsis when it does not fit.
func Truncate(s string, width int) string {
	if width <= 1 || Width(s) <= width {
		return s
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	return string(runes[:width-1]) + "…"
}

// Bytes renders a byte count in human units.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit && exp < 4; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTP"[exp])
}

// Ago renders how long ago t was, compactly.
func Ago(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dmo ago", int(d.Hours()/24/30))
	}
}

// Duration renders a compact elapsed time.
func Duration(d time.Duration) string {
	switch {
	case d < time.Second:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

// Table accumulates rows and prints them in aligned columns.
type Table struct {
	rows [][]string
}

// Row appends one row.
func (t *Table) Row(cells ...string) { t.rows = append(t.rows, cells) }

// Render writes the aligned table, prefixing every line with indent.
func (t *Table) Render(indent string) string {
	if len(t.rows) == 0 {
		return ""
	}
	widths := []int{}
	for _, row := range t.rows {
		for i, cell := range row {
			for len(widths) <= i {
				widths = append(widths, 0)
			}
			if w := Width(cell); w > widths[i] {
				widths[i] = w
			}
		}
	}
	var b strings.Builder
	for _, row := range t.rows {
		var line strings.Builder
		line.WriteString(indent)
		for i, cell := range row {
			if i == len(row)-1 {
				line.WriteString(cell)
			} else {
				line.WriteString(Pad(cell, widths[i]) + "  ")
			}
		}
		b.WriteString(strings.TrimRight(line.String(), " "))
		b.WriteString("\n")
	}
	return b.String()
}
