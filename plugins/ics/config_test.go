package ics

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"google.golang.org/protobuf/types/known/structpb"
)

func mustStruct(t *testing.T, m map[string]any) *structpb.Struct {
	t.Helper()
	s, err := structpb.NewStruct(m)
	if err != nil {
		t.Fatalf("build struct: %v", err)
	}
	return s
}

func TestParseConfig(t *testing.T) {
	t.Run("valid http with default horizon", func(t *testing.T) {
		cfg, err := ParseConfig(mustStruct(t, map[string]any{"url": "https://example.com/cal.ics"}))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.URL != "https://example.com/cal.ics" || cfg.HorizonDays != DefaultHorizonDays {
			t.Fatalf("cfg = %+v", cfg)
		}
	})
	t.Run("valid file with explicit horizon", func(t *testing.T) {
		cfg, err := ParseConfig(mustStruct(t, map[string]any{"url": "file:///tmp/cal.ics", "horizon_days": 30}))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.URL != "file:///tmp/cal.ics" || cfg.HorizonDays != 30 {
			t.Fatalf("cfg = %+v", cfg)
		}
	})

	errCases := []struct {
		name    string
		s       *structpb.Struct
		wantSub string
	}{
		{"nil struct", nil, "missing config"},
		{"missing url", mustStruct(t, map[string]any{"horizon_days": 30}), `"url" is missing`},
		{"url wrong type", mustStruct(t, map[string]any{"url": 12}), "must be a string"},
		{"empty url", mustStruct(t, map[string]any{"url": "  "}), "must not be empty"},
		{"bad scheme", mustStruct(t, map[string]any{"url": "ftp://example.com/cal.ics"}), `unsupported URL scheme "ftp"`},
		{"file url without path", mustStruct(t, map[string]any{"url": "file://"}), "absolute path"},
		{"http url without host", mustStruct(t, map[string]any{"url": "http://"}), "no host"},
		{"horizon wrong type", mustStruct(t, map[string]any{"url": "https://e.com/c.ics", "horizon_days": "60"}), "must be a number"},
		{"horizon zero", mustStruct(t, map[string]any{"url": "https://e.com/c.ics", "horizon_days": 0}), "between 1 and 3650"},
		{"horizon negative", mustStruct(t, map[string]any{"url": "https://e.com/c.ics", "horizon_days": -5}), "between 1 and 3650"},
		{"horizon fractional", mustStruct(t, map[string]any{"url": "https://e.com/c.ics", "horizon_days": 1.5}), "whole number"},
		{"unknown field", mustStruct(t, map[string]any{"url": "https://e.com/c.ics", "horizonDays": 30}), "unknown field(s) horizonDays"},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseConfig(tc.s)
			if err == nil {
				t.Fatal("ParseConfig accepted an invalid config")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not mention %q", err, tc.wantSub)
			}
		})
	}
}

func TestFetchFile(t *testing.T) {
	want := []byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")
	path := filepath.Join(t.TempDir(), "cal.ics")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Fetch(context.Background(), nil, Config{URL: "file://" + path})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Fetch = %q, want %q", got, want)
	}

	if _, err := Fetch(context.Background(), nil, Config{URL: "file:///nonexistent/cal.ics"}); err == nil {
		t.Error("Fetch of a missing file did not fail")
	}
	if _, err := Fetch(context.Background(), nil, Config{URL: "file://remotehost/cal.ics"}); err == nil {
		t.Error("Fetch of a remote-host file URL did not fail")
	}
}

func TestFetchHTTP(t *testing.T) {
	want := []byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing.ics" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/calendar")
		_, _ = w.Write(want)
	}))
	defer srv.Close()

	// nil client exercises the http.DefaultClient fallback.
	got, err := Fetch(context.Background(), nil, Config{URL: srv.URL + "/cal.ics"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Fetch = %q, want %q", got, want)
	}

	_, err = Fetch(context.Background(), srv.Client(), Config{URL: srv.URL + "/missing.ics"})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("Fetch of a 404 = %v, want a status error", err)
	}
}

func TestFetchHTTPS(t *testing.T) {
	want := []byte("BEGIN:VCALENDAR\r\nEND:VCALENDAR\r\n")
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(want)
	}))
	defer srv.Close()

	got, err := Fetch(context.Background(), srv.Client(), Config{URL: srv.URL + "/cal.ics"})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("Fetch = %q, want %q", got, want)
	}
}

func TestFetchRejectsOtherSchemes(t *testing.T) {
	for _, u := range []string{"ftp://example.com/cal.ics", "gopher://example.com/cal", "mailto:someone@example.com"} {
		if _, err := Fetch(context.Background(), nil, Config{URL: u}); err == nil {
			t.Errorf("Fetch(%q) did not fail", u)
		} else if !strings.Contains(err.Error(), "unsupported URL scheme") {
			t.Errorf("Fetch(%q) error = %v, want an unsupported-scheme error", u, err)
		}
	}
}
