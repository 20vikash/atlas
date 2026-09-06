package api

import "time"

// formatRFC3339 renders a timestamp, or "" when it is unset. A zero time would
// otherwise appear as a real date in the year 1.
func formatRFC3339(timestamp time.Time) string {
	if timestamp.IsZero() {
		return ""
	}

	return timestamp.Format(time.RFC3339)
}
