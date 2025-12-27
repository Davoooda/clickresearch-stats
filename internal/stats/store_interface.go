package stats

import (
	"context"
	"time"
)

// StoreInterface defines the analytics store contract
type StoreInterface interface {
	Close() error
	GetOverview(ctx context.Context, domain string, from, to time.Time) (*Overview, error)
	GetPageviewsTimeSeries(ctx context.Context, domain string, from, to time.Time, interval string) ([]TimeSeriesPoint, error)
	GetTopPages(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error)
	GetTopSources(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error)
	GetTopBrowsers(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error)
	GetTopCountries(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error)
	GetTopDevices(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error)
	GetTopUTMSources(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error)
	GetTopUTMMediums(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error)
	GetTopUTMCampaigns(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error)
	GetRecentEvents(ctx context.Context, domain string, from, to time.Time, limit int) ([]EventItem, error)
	GetEventBreakdown(ctx context.Context, domain string, from, to time.Time) ([]TopItem, error)
	GetUniquePages(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error)
	GetFunnel(ctx context.Context, domain string, from, to time.Time, steps []string) (*FunnelResult, error)
	GetFunnelAdvanced(ctx context.Context, domain string, from, to time.Time, steps []FunnelStepDef, windowMinutes int) (*FunnelResult, error)
	GetAutocaptureEvents(ctx context.Context, domain string, from, to time.Time, limit int) ([]AutocaptureEvent, error)
}
