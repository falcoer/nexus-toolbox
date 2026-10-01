package ui

import (
	"fmt"
	"time"
)

// HumanSize renders a byte count (12.4 MiB).
func HumanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// HumanAgo renders a relative date (il y a 3 j).
func HumanAgo(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "à l'instant"
	case d < time.Hour:
		return fmt.Sprintf("il y a %d min", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("il y a %d h", int(d.Hours()))
	case d < 60*24*time.Hour:
		return fmt.Sprintf("il y a %d j", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02")
	}
}
