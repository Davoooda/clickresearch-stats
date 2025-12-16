package stats

import (
	"testing"
)

func TestCleanReferrer(t *testing.T) {
	tests := []struct {
		referrer string
		domain   string
		expected string
	}{
		{"", "example.com", "Direct"},
		{"https://google.com/search?q=test", "example.com", "google.com"},
		{"https://facebook.com/share", "example.com", "facebook.com"},
		{"https://example.com/page", "example.com", "Direct"}, // internal
		{"https://sub.example.com/page", "example.com", "Direct"}, // internal subdomain
		{"invalid-url", "example.com", "Direct"},
		{"https://twitter.com", "example.com", "twitter.com"},
	}

	for _, tt := range tests {
		t.Run(tt.referrer, func(t *testing.T) {
			got := cleanReferrer(tt.referrer, tt.domain)
			if got != tt.expected {
				t.Errorf("cleanReferrer(%q, %q) = %q, want %q", tt.referrer, tt.domain, got, tt.expected)
			}
		})
	}
}

func TestTopN(t *testing.T) {
	counts := map[string]int64{
		"page1": 100,
		"page2": 50,
		"page3": 200,
		"page4": 75,
		"page5": 25,
	}

	tests := []struct {
		n        int
		expected int
		first    string
	}{
		{3, 3, "page3"},
		{5, 5, "page3"},
		{10, 5, "page3"}, // more than available
		{1, 1, "page3"},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			result := topN(counts, tt.n)
			if len(result) != tt.expected {
				t.Errorf("topN(counts, %d) returned %d items, want %d", tt.n, len(result), tt.expected)
			}
			if len(result) > 0 && result[0].Name != tt.first {
				t.Errorf("First item = %s, want %s", result[0].Name, tt.first)
			}
		})
	}
}

func TestTopN_Sorted(t *testing.T) {
	counts := map[string]int64{
		"a": 10,
		"b": 30,
		"c": 20,
	}

	result := topN(counts, 3)

	if result[0].Count != 30 || result[1].Count != 20 || result[2].Count != 10 {
		t.Errorf("topN should be sorted descending: %v", result)
	}
}

func TestTopN_Empty(t *testing.T) {
	counts := map[string]int64{}
	result := topN(counts, 10)
	if len(result) != 0 {
		t.Errorf("topN on empty map should return empty slice")
	}
}

func TestMatchesStep(t *testing.T) {
	tests := []struct {
		pathname string
		step     string
		expected bool
	}{
		{"/dashboard", "/dashboard", true},
		{"/dashboard/settings", "/dashboard", false},
		{"/dashboard/settings", "/dashboard/*", true},
		{"/dashboard", "/dashboard/*", false}, // no trailing content
		{"/", "/", true},
		{"/page", "/other", false},
		{"/api/v1/users", "/api/*", true},
	}

	for _, tt := range tests {
		t.Run(tt.pathname+"_"+tt.step, func(t *testing.T) {
			got := matchesStep(tt.pathname, tt.step)
			if got != tt.expected {
				t.Errorf("matchesStep(%q, %q) = %v, want %v", tt.pathname, tt.step, got, tt.expected)
			}
		})
	}
}

func TestExtractJSONField(t *testing.T) {
	tests := []struct {
		json     string
		field    string
		expected string
	}{
		{`{"text":"hello","tag":"button"}`, "text", "hello"},
		{`{"text":"hello","tag":"button"}`, "tag", "button"},
		{`{"text":"hello","tag":"button"}`, "missing", ""},
		{`{"text": "spaced"}`, "text", "spaced"},
		{`{}`, "text", ""},
		{`{"nested":{"text":"inner"}}`, "text", "inner"},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			got := extractJSONField(tt.json, tt.field)
			if got != tt.expected {
				t.Errorf("extractJSONField(%q, %q) = %q, want %q", tt.json, tt.field, got, tt.expected)
			}
		})
	}
}

func TestMatchesStepDef_Pageview(t *testing.T) {
	event := Event{
		Name:     "pageview",
		Pathname: "/dashboard",
	}

	tests := []struct {
		step     FunnelStepDef
		expected bool
	}{
		{FunnelStepDef{Type: "pageview", Value: "/dashboard"}, true},
		{FunnelStepDef{Type: "pageview", Value: "/other"}, false},
		{FunnelStepDef{Type: "pageview", Value: "/dash*"}, true},
		{FunnelStepDef{Type: "event", Value: "pageview"}, true}, // matches event.Name
		{FunnelStepDef{Type: "event", Value: "click"}, false},   // different event
	}

	for _, tt := range tests {
		t.Run(tt.step.Value, func(t *testing.T) {
			got := matchesStepDef(event, tt.step)
			if got != tt.expected {
				t.Errorf("matchesStepDef = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMatchesStepDef_Event(t *testing.T) {
	event := Event{
		Name:  "click",
		Props: `{"text":"Submit","tag":"button"}`,
	}

	tests := []struct {
		step     FunnelStepDef
		expected bool
	}{
		{FunnelStepDef{Type: "event", Value: "click"}, true},
		{FunnelStepDef{Type: "event", Value: "click", Text: "Submit"}, true},
		{FunnelStepDef{Type: "event", Value: "click", Text: "Cancel"}, false},
		{FunnelStepDef{Type: "event", Value: "click", Tag: "button"}, true},
		{FunnelStepDef{Type: "event", Value: "click", Tag: "a"}, false},
		{FunnelStepDef{Type: "event", Value: "submit"}, false}, // wrong event
		{FunnelStepDef{Type: "pageview", Value: "/page"}, false}, // wrong type
	}

	for _, tt := range tests {
		t.Run(tt.step.Value+"_"+tt.step.Text, func(t *testing.T) {
			got := matchesStepDef(event, tt.step)
			if got != tt.expected {
				t.Errorf("matchesStepDef = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestOverview_Struct(t *testing.T) {
	o := Overview{
		Pageviews:      100,
		UniqueVisitors: 50,
		Events:         150,
	}

	if o.Pageviews != 100 {
		t.Errorf("Pageviews = %d, want 100", o.Pageviews)
	}
}

func TestFunnelStep_Struct(t *testing.T) {
	step := FunnelStep{
		Name:    "/checkout",
		Count:   42,
		Percent: 85.5,
	}

	if step.Name != "/checkout" {
		t.Errorf("Name = %s, want /checkout", step.Name)
	}
}

func TestTopItem_Struct(t *testing.T) {
	item := TopItem{
		Name:  "/dashboard",
		Count: 1000,
	}

	if item.Count != 1000 {
		t.Errorf("Count = %d, want 1000", item.Count)
	}
}

func TestTimeSeriesPoint_Struct(t *testing.T) {
	point := TimeSeriesPoint{
		Time:  "2024-01-15T10:00",
		Value: 250,
	}

	if point.Time != "2024-01-15T10:00" {
		t.Errorf("Time = %s, want 2024-01-15T10:00", point.Time)
	}
}

func TestEventItem_Struct(t *testing.T) {
	item := EventItem{
		Name:      "pageview",
		URL:       "https://example.com/page",
		Pathname:  "/page",
		Country:   "US",
		Browser:   "Chrome",
		OS:        "Windows",
		Device:    "desktop",
		Timestamp: "2024-01-15 10:30:00",
	}

	if item.Name != "pageview" {
		t.Errorf("Name = %s, want pageview", item.Name)
	}
}

func TestAutocaptureEvent_Struct(t *testing.T) {
	event := AutocaptureEvent{
		EventType: "click",
		Text:      "Submit",
		Tag:       "button",
		Pathname:  "/form",
		Count:     42,
	}

	if event.EventType != "click" {
		t.Errorf("EventType = %s, want click", event.EventType)
	}
}
