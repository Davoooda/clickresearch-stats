package stats

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/shortid/clickresearch-stats/internal/cache"
)

type Handler struct {
	store *Store
	cache *cache.Cache
}

func NewHandler(store *Store) *Handler {
	return &Handler{
		store: store,
		cache: cache.New(5 * time.Minute), // 5 min TTL
	}
}

// parseParams extracts common query parameters
func parseParams(r *http.Request) (domain string, from, to time.Time) {
	domain = r.URL.Query().Get("domain")
	if domain == "" {
		domain = "shortid.me"
	}

	to = time.Now().UTC()
	from = to.AddDate(0, 0, -7)

	if period := r.URL.Query().Get("period"); period != "" {
		switch period {
		case "today":
			from = time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
		case "7d":
			from = to.AddDate(0, 0, -7)
		case "30d":
			from = to.AddDate(0, 0, -30)
		case "90d":
			from = to.AddDate(0, 0, -90)
		}
	}

	return domain, from, to
}

func writeJSON(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, err error, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	msg := "unknown error"
	if err != nil {
		msg = err.Error()
	} else if code == http.StatusServiceUnavailable {
		msg = "stats not available"
	}
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func (h *Handler) HandleOverview(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, nil, http.StatusServiceUnavailable)
		return
	}

	domain, from, to := parseParams(r)
	cacheKey := fmt.Sprintf("overview:%s:%s", domain, r.URL.Query().Get("period"))

	// Try cache first
	var data *Overview
	if h.cache.Get(cacheKey, &data) {
		writeJSON(w, data)
		return
	}

	// Cache miss - fetch and cache
	data, err := h.store.GetOverview(r.Context(), domain, from, to)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	h.cache.Set(cacheKey, data)
	writeJSON(w, data)
}

func (h *Handler) HandlePageviews(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, nil, http.StatusServiceUnavailable)
		return
	}

	domain, from, to := parseParams(r)
	interval := "hour"
	if to.Sub(from) > 7*24*time.Hour {
		interval = "day"
	}

	cacheKey := fmt.Sprintf("pageviews:%s:%s", domain, r.URL.Query().Get("period"))
	var data []TimeSeriesPoint
	if h.cache.Get(cacheKey, &data) {
		writeJSON(w, data)
		return
	}

	data, err := h.store.GetPageviewsTimeSeries(r.Context(), domain, from, to, interval)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	h.cache.Set(cacheKey, data)
	writeJSON(w, data)
}

func (h *Handler) HandlePages(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, nil, http.StatusServiceUnavailable)
		return
	}

	domain, from, to := parseParams(r)
	limit := parseLimit(r, 10)

	cacheKey := fmt.Sprintf("pages:%s:%s:%d", domain, r.URL.Query().Get("period"), limit)
	var data []TopItem
	if h.cache.Get(cacheKey, &data) {
		writeJSON(w, data)
		return
	}

	data, err := h.store.GetTopPages(r.Context(), domain, from, to, limit)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	h.cache.Set(cacheKey, data)
	writeJSON(w, data)
}

func (h *Handler) HandleSources(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, nil, http.StatusServiceUnavailable)
		return
	}

	domain, from, to := parseParams(r)
	limit := parseLimit(r, 10)

	data, err := h.store.GetTopSources(r.Context(), domain, from, to, limit)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, data)
}

func (h *Handler) HandleDevices(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, nil, http.StatusServiceUnavailable)
		return
	}

	domain, from, to := parseParams(r)
	limit := parseLimit(r, 10)

	browsers, err := h.store.GetTopBrowsers(r.Context(), domain, from, to, limit)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}

	devices, err := h.store.GetTopDevices(r.Context(), domain, from, to, limit)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}

	writeJSON(w, map[string]any{
		"browsers": browsers,
		"devices":  devices,
	})
}

func (h *Handler) HandleGeo(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, nil, http.StatusServiceUnavailable)
		return
	}

	domain, from, to := parseParams(r)
	limit := parseLimit(r, 10)

	data, err := h.store.GetTopCountries(r.Context(), domain, from, to, limit)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, data)
}

func parseLimit(r *http.Request, defaultVal int) int {
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultVal
}

func (h *Handler) HandleEvents(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, nil, http.StatusServiceUnavailable)
		return
	}

	domain, from, to := parseParams(r)
	limit := parseLimit(r, 50)

	data, err := h.store.GetRecentEvents(r.Context(), domain, from, to, limit)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, data)
}

func (h *Handler) HandleFunnel(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, nil, http.StatusServiceUnavailable)
		return
	}

	domain, from, to := parseParams(r)

	// Parse steps from query param (comma-separated)
	stepsParam := r.URL.Query().Get("steps")
	if stepsParam == "" {
		// Default funnel: homepage -> dashboard
		stepsParam = "/,/dashboard/"
	}

	steps := []string{}
	for _, s := range splitSteps(stepsParam) {
		if s != "" {
			steps = append(steps, s)
		}
	}

	if len(steps) < 2 {
		writeError(w, nil, http.StatusBadRequest)
		return
	}

	data, err := h.store.GetFunnel(r.Context(), domain, from, to, steps)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, data)
}

func splitSteps(s string) []string {
	var result []string
	current := ""
	for _, c := range s {
		if c == ',' {
			result = append(result, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

func (h *Handler) HandleEventBreakdown(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, nil, http.StatusServiceUnavailable)
		return
	}

	domain, from, to := parseParams(r)
	data, err := h.store.GetEventBreakdown(r.Context(), domain, from, to)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, data)
}

func (h *Handler) HandleUniquePages(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, nil, http.StatusServiceUnavailable)
		return
	}

	domain, from, to := parseParams(r)
	limit := parseLimit(r, 100)

	data, err := h.store.GetUniquePages(r.Context(), domain, from, to, limit)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, data)
}

func (h *Handler) HandleAutocaptureEvents(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, nil, http.StatusServiceUnavailable)
		return
	}

	domain, from, to := parseParams(r)
	limit := parseLimit(r, 100)

	data, err := h.store.GetAutocaptureEvents(r.Context(), domain, from, to, limit)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, data)
}

// FunnelAdvancedRequest is the request body for advanced funnel
type FunnelAdvancedRequest struct {
	Steps  []FunnelStepDef `json:"steps"`
	Window int             `json:"window"` // minutes
}

func (h *Handler) HandleFunnelAdvanced(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		writeError(w, nil, http.StatusServiceUnavailable)
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, nil, http.StatusMethodNotAllowed)
		return
	}

	domain, from, to := parseParams(r)

	var req FunnelAdvancedRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}

	if len(req.Steps) < 2 {
		writeError(w, nil, http.StatusBadRequest)
		return
	}

	// Default window to 60 minutes if not specified
	window := req.Window
	if window <= 0 {
		window = 60
	}

	data, err := h.store.GetFunnelAdvanced(r.Context(), domain, from, to, req.Steps, window)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, data)
}
