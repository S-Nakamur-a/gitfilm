package tui

import "time"

func mustParse(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func hour(n int) time.Duration { return time.Duration(n) * time.Hour }
