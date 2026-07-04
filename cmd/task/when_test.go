package main

import (
	"testing"
	"time"
)

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, time.July, 4, 10, 30, 0, 0, time.Local)
	cases := []struct {
		in      string
		want    time.Time
		wantErr bool
	}{
		{in: "today", want: time.Date(2026, time.July, 4, 23, 59, 59, 0, time.Local)},
		{in: "Today", want: time.Date(2026, time.July, 4, 23, 59, 59, 0, time.Local)},
		{in: "tomorrow", want: time.Date(2026, time.July, 5, 23, 59, 59, 0, time.Local)},
		{in: "0d", want: now},
		{in: "3d", want: time.Date(2026, time.July, 7, 10, 30, 0, 0, time.Local)},
		{in: "30d", want: time.Date(2026, time.August, 3, 10, 30, 0, 0, time.Local)},
		{in: "2026-12-24", want: time.Date(2026, time.December, 24, 23, 59, 59, 0, time.Local)},
		{in: "2026-12-24 08:15", want: time.Date(2026, time.December, 24, 8, 15, 0, 0, time.Local)},
		{in: "", wantErr: true},
		{in: "next week", wantErr: true},
		{in: "-3d", wantErr: true},
		{in: "3h", wantErr: true},
		{in: "2026-13-01", wantErr: true},
		{in: "2026-12-24 25:00", wantErr: true},
	}
	for _, c := range cases {
		got, err := parseWhen(c.in, now)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseWhen(%q) = %v, want error", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseWhen(%q): %v", c.in, err)
			continue
		}
		if !got.Equal(c.want) {
			t.Errorf("parseWhen(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
