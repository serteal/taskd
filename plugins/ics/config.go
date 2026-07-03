package ics

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"
)

// DefaultHorizonDays is the instance materialization window (±days around
// "now") used when the config does not set one (DESIGN.md §6: rolling
// horizon, default ±60d).
const DefaultHorizonDays = 60

// Config is the per-instance connector configuration.
type Config struct {
	// URL locates the calendar: file:///abs/path.ics or http(s)://host/x.ics.
	URL string
	// HorizonDays is the instance materialization window in days on each
	// side of "now"; DefaultHorizonDays when unset.
	HorizonDays int
}

// ParseConfig validates the instance config struct. Accepted fields:
//
//	url          (string, required)  file://, http:// or https:// URL
//	horizon_days (number, optional)  whole number of days in [1, 3650];
//	                                 defaults to DefaultHorizonDays
//
// Unknown fields are rejected so config typos surface at configure time.
func ParseConfig(s *structpb.Struct) (Config, error) {
	if s == nil {
		return Config{}, errors.New(`ics config: missing config (need at least {"url": ...})`)
	}
	fields := s.GetFields()

	var unknown []string
	for k := range fields {
		if k != "url" && k != "horizon_days" {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return Config{}, fmt.Errorf("ics config: unknown field(s) %s (accepted: url, horizon_days)",
			strings.Join(unknown, ", "))
	}

	urlField, ok := fields["url"]
	if !ok {
		return Config{}, errors.New(`ics config: required field "url" is missing`)
	}
	sv, ok := urlField.GetKind().(*structpb.Value_StringValue)
	if !ok {
		return Config{}, errors.New(`ics config: field "url" must be a string`)
	}
	raw := strings.TrimSpace(sv.StringValue)
	if raw == "" {
		return Config{}, errors.New(`ics config: field "url" must not be empty`)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return Config{}, fmt.Errorf(`ics config: field "url" is not a valid URL: %v`, err)
	}
	switch u.Scheme {
	case "file":
		if u.Path == "" {
			return Config{}, fmt.Errorf(`ics config: file URL %q must carry an absolute path (file:///path/to/cal.ics)`, raw)
		}
	case "http", "https":
		if u.Host == "" {
			return Config{}, fmt.Errorf(`ics config: URL %q has no host`, raw)
		}
	default:
		return Config{}, fmt.Errorf(`ics config: unsupported URL scheme %q in %q (want file, http, or https)`, u.Scheme, raw)
	}

	cfg := Config{URL: raw, HorizonDays: DefaultHorizonDays}
	if hv, ok := fields["horizon_days"]; ok {
		nv, ok := hv.GetKind().(*structpb.Value_NumberValue)
		if !ok {
			return Config{}, errors.New(`ics config: field "horizon_days" must be a number`)
		}
		n := nv.NumberValue
		if n != math.Trunc(n) || n < 1 || n > 3650 {
			return Config{}, fmt.Errorf(`ics config: field "horizon_days" must be a whole number of days between 1 and 3650, got %v`, n)
		}
		cfg.HorizonDays = int(n)
	}
	return cfg, nil
}

// Fetch retrieves the raw ICS payload for cfg.URL.
//
// file:// URLs read the local file (no remote host allowed); http(s):// URLs
// issue a GET through client (http.DefaultClient when nil) and require a 2xx
// response. Any other scheme is rejected.
func Fetch(ctx context.Context, client *http.Client, cfg Config) ([]byte, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("ics fetch: parse url %q: %v", cfg.URL, err)
	}
	switch u.Scheme {
	case "file":
		if u.Host != "" && u.Host != "localhost" {
			return nil, fmt.Errorf("ics fetch: file URL %q must not name a remote host", cfg.URL)
		}
		if u.Path == "" {
			return nil, fmt.Errorf("ics fetch: file URL %q has no path", cfg.URL)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		b, err := os.ReadFile(u.Path)
		if err != nil {
			return nil, fmt.Errorf("ics fetch: %w", err)
		}
		return b, nil
	case "http", "https":
		if client == nil {
			client = http.DefaultClient
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.URL, nil)
		if err != nil {
			return nil, fmt.Errorf("ics fetch: build request for %q: %v", cfg.URL, err)
		}
		req.Header.Set("Accept", "text/calendar")
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("ics fetch: GET %s: %w", cfg.URL, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, fmt.Errorf("ics fetch: GET %s: unexpected status %s", cfg.URL, resp.Status)
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("ics fetch: read body of %s: %v", cfg.URL, err)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("ics fetch: unsupported URL scheme %q in %q (want file, http, or https)", u.Scheme, cfg.URL)
	}
}
