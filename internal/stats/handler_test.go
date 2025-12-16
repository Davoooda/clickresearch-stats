package stats

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseParams_Default(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/stats/overview", nil)
	domain, from, to := parseParams(req)

	if domain != "shortid.me" {
		t.Errorf("domain = %s, want shortid.me", domain)
	}

	expectedFrom := time.Now().UTC().AddDate(0, 0, -7)
	if from.Day() != expectedFrom.Day() {
		t.Errorf("from day = %d, want %d", from.Day(), expectedFrom.Day())
	}

	if to.Day() != time.Now().UTC().Day() {
		t.Errorf("to should be today")
	}
}

func TestParseParams_CustomDomain(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/stats/overview?domain=example.com", nil)
	domain, _, _ := parseParams(req)

	if domain != "example.com" {
		t.Errorf("domain = %s, want example.com", domain)
	}
}

func TestParseParams_Periods(t *testing.T) {
	tests := []struct {
		period      string
		expectedAge int // days ago
	}{
		{"today", 0},
		{"7d", 7},
		{"30d", 30},
		{"90d", 90},
	}

	for _, tt := range tests {
		t.Run(tt.period, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/stats/overview?period="+tt.period, nil)
			_, from, to := parseParams(req)

			diff := int(to.Sub(from).Hours() / 24)
			if tt.period == "today" {
				// today is special - from start of day
				if from.Hour() != 0 || from.Minute() != 0 {
					t.Errorf("today should start at midnight")
				}
			} else if diff != tt.expectedAge {
				t.Errorf("period %s: diff = %d days, want %d", tt.period, diff, tt.expectedAge)
			}
		})
	}
}

func TestParseLimit(t *testing.T) {
	tests := []struct {
		query    string
		def      int
		expected int
	}{
		{"", 10, 10},
		{"?limit=5", 10, 5},
		{"?limit=100", 10, 100},
		{"?limit=abc", 10, 10},
		{"?limit=-1", 10, 10},
		{"?limit=0", 10, 10},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/stats/pages"+tt.query, nil)
			got := parseLimit(req, tt.def)
			if got != tt.expected {
				t.Errorf("parseLimit(%q, %d) = %d, want %d", tt.query, tt.def, got, tt.expected)
			}
		})
	}
}

func TestSplitSteps(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{"/,/dashboard/", []string{"/", "/dashboard/"}},
		{"/a,/b,/c", []string{"/a", "/b", "/c"}},
		{"single", []string{"single"}},
		{"", []string{}},
		{"/path/with/slash,/other", []string{"/path/with/slash", "/other"}},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := splitSteps(tt.input)
			if len(got) != len(tt.expected) {
				t.Errorf("splitSteps(%q) = %v, want %v", tt.input, got, tt.expected)
				return
			}
			for i := range got {
				if got[i] != tt.expected[i] {
					t.Errorf("splitSteps(%q)[%d] = %s, want %s", tt.input, i, got[i], tt.expected[i])
				}
			}
		})
	}
}

func TestHandler_NilStore(t *testing.T) {
	h := &Handler{store: nil}

	endpoints := []struct {
		name    string
		handler http.HandlerFunc
	}{
		{"overview", h.HandleOverview},
		{"pageviews", h.HandlePageviews},
		{"pages", h.HandlePages},
		{"sources", h.HandleSources},
		{"devices", h.HandleDevices},
		{"geo", h.HandleGeo},
		{"events", h.HandleEvents},
		{"funnel", h.HandleFunnel},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/stats/"+ep.name, nil)
			w := httptest.NewRecorder()

			ep.handler(w, req)

			if w.Code != http.StatusServiceUnavailable {
				t.Errorf("%s: status = %d, want %d", ep.name, w.Code, http.StatusServiceUnavailable)
			}
		})
	}
}

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	data := map[string]string{"key": "value"}

	writeJSON(w, data)

	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type = %s, want application/json", w.Header().Get("Content-Type"))
	}

	if w.Body.String() != "{\"key\":\"value\"}\n" {
		t.Errorf("Body = %s, want {\"key\":\"value\"}", w.Body.String())
	}
}

func TestWriteError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		code     int
		expected string
	}{
		{"with error", http.ErrBodyNotAllowed, 500, "http: request method or response status code does not allow body"},
		{"nil error 503", nil, 503, "stats not available"},
		{"nil error 400", nil, 400, "unknown error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeError(w, tt.err, tt.code)

			if w.Code != tt.code {
				t.Errorf("status = %d, want %d", w.Code, tt.code)
			}
		})
	}
}
