package stats

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	_ "github.com/marcboeker/go-duckdb"
)

type Store struct {
	db             *sql.DB
	mu             sync.Mutex
	parquetPath    string
	ready          bool
	useMemoryTable bool
}

type Config struct {
	S3Endpoint string
	S3Key      string
	S3Secret   string
	Bucket     string
	Prefix     string
}

func NewStore(cfg Config) (*Store, error) {
	db, err := sql.Open("duckdb", "?threads=2&memory_limit=512MB")
	if err != nil {
		return nil, fmt.Errorf("failed to open duckdb: %w", err)
	}

	s := &Store{
		db:          db,
		parquetPath: fmt.Sprintf("s3://%s/%s**/*.parquet", cfg.Bucket, cfg.Prefix),
	}

	// Initialize S3 access
	go s.initS3(cfg)

	return s, nil
}

func (s *Store) initS3(cfg Config) {
	log.Println("DuckDB: initializing S3 access...")

	queries := []string{
		"INSTALL httpfs",
		"LOAD httpfs",
		fmt.Sprintf("SET s3_endpoint='%s'", cfg.S3Endpoint),
		fmt.Sprintf("SET s3_access_key_id='%s'", cfg.S3Key),
		fmt.Sprintf("SET s3_secret_access_key='%s'", cfg.S3Secret),
		"SET s3_url_style='path'",
		"SET s3_use_ssl=true",
	}

	for _, q := range queries {
		if _, err := s.db.Exec(q); err != nil {
			log.Printf("DuckDB setup error: %v", err)
			return
		}
	}

	// Initial load
	s.refreshMemoryTable()

	s.ready = true
	log.Println("DuckDB: S3 access initialized successfully")

	// Periodic refresh every 5 minutes
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		for range ticker.C {
			s.refreshMemoryTable()
		}
	}()
}

func (s *Store) refreshMemoryTable() {
	s.mu.Lock()
	defer s.mu.Unlock()

	log.Println("DuckDB: refreshing data from S3...")

	// Drop and recreate table
	s.db.Exec("DROP TABLE IF EXISTS events")

	createTable := fmt.Sprintf(`
		CREATE TABLE events AS
		SELECT * FROM read_parquet('%s')
	`, s.parquetPath)

	if _, err := s.db.Exec(createTable); err != nil {
		log.Printf("DuckDB: failed to refresh memory table: %v", err)
		s.useMemoryTable = false
	} else {
		s.useMemoryTable = true
		log.Println("DuckDB: data refreshed")
	}
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) tableSource() string {
	if s.useMemoryTable {
		return "events"
	}
	return fmt.Sprintf("read_parquet('%s')", s.parquetPath)
}

// Overview stats
type Overview struct {
	Pageviews      int64 `json:"pageviews"`
	UniqueVisitors int64 `json:"unique_visitors"`
	Events         int64 `json:"events"`
}

func (s *Store) GetOverview(ctx context.Context, domain string, from, to time.Time) (*Overview, error) {
	if !s.ready {
		return &Overview{}, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	query := fmt.Sprintf(`
		SELECT
			COUNT(*) FILTER (WHERE name = 'pageview') as pageviews,
			COUNT(DISTINCT visitor_id) as unique_visitors,
			COUNT(*) as events
		FROM %s
		WHERE domain = $1
		AND epoch_us(timestamp) >= $2
		AND epoch_us(timestamp) < $3
	`, s.tableSource())

	var o Overview
	err := s.db.QueryRowContext(ctx, query, domain, from.UnixMicro(), to.UnixMicro()).Scan(
		&o.Pageviews, &o.UniqueVisitors, &o.Events,
	)
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// TimeSeriesPoint for charts
type TimeSeriesPoint struct {
	Time  string `json:"time"`
	Value int64  `json:"value"`
}

func (s *Store) GetPageviewsTimeSeries(ctx context.Context, domain string, from, to time.Time, interval string) ([]TimeSeriesPoint, error) {
	if !s.ready {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	dateFormat := "date_trunc('day', timestamp::timestamp)"
	if interval == "hour" {
		dateFormat = "date_trunc('hour', timestamp::timestamp)"
	}

	query := fmt.Sprintf(`
		SELECT
			%s as time_bucket,
			COUNT(*) as count
		FROM %s
		WHERE domain = $1
		AND name = 'pageview'
		AND epoch_us(timestamp) >= $2
		AND epoch_us(timestamp) < $3
		GROUP BY time_bucket
		ORDER BY time_bucket
	`, dateFormat, s.tableSource())

	rows, err := s.db.QueryContext(ctx, query, domain, from.UnixMicro(), to.UnixMicro())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []TimeSeriesPoint
	for rows.Next() {
		var t time.Time
		var count int64
		if err := rows.Scan(&t, &count); err != nil {
			continue
		}
		format := "2006-01-02"
		if interval == "hour" {
			format = "2006-01-02T15:00"
		}
		result = append(result, TimeSeriesPoint{Time: t.Format(format), Value: count})
	}
	return result, nil
}

// TopItem for rankings
type TopItem struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}

// PageItem is alias for TopItem for unique pages
type PageItem = TopItem

func (s *Store) GetTopPages(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	return s.getTopBy(ctx, "pathname", "pageview", domain, from, to, limit)
}

func (s *Store) GetTopSources(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	if !s.ready {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	query := fmt.Sprintf(`
		SELECT
			CASE
				WHEN referrer = '' OR referrer IS NULL THEN 'Direct'
				WHEN referrer LIKE '%%' || $1 || '%%' THEN 'Direct'
				ELSE regexp_extract(referrer, 'https?://([^/]+)', 1)
			END as source,
			COUNT(*) as count
		FROM %s
		WHERE domain = $1
		AND name = 'pageview'
		AND epoch_us(timestamp) >= $2
		AND epoch_us(timestamp) < $3
		GROUP BY source
		ORDER BY count DESC
		LIMIT $4
	`, s.tableSource())

	rows, err := s.db.QueryContext(ctx, query, domain, from.UnixMicro(), to.UnixMicro(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanTopItems(rows)
}

func (s *Store) GetTopBrowsers(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	return s.getTopBy(ctx, "browser", "", domain, from, to, limit)
}

func (s *Store) GetTopCountries(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	return s.getTopBy(ctx, "country", "", domain, from, to, limit)
}

func (s *Store) GetTopDevices(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	return s.getTopBy(ctx, "device", "", domain, from, to, limit)
}

func (s *Store) getTopBy(ctx context.Context, field, eventFilter, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	if !s.ready {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	eventClause := ""
	if eventFilter != "" {
		eventClause = fmt.Sprintf("AND name = '%s'", eventFilter)
	}

	query := fmt.Sprintf(`
		SELECT
			COALESCE(NULLIF(%s, ''), 'Unknown') as name,
			COUNT(*) as count
		FROM %s
		WHERE domain = $1
		%s
		AND epoch_us(timestamp) >= $2
		AND epoch_us(timestamp) < $3
		GROUP BY 1
		ORDER BY count DESC
		LIMIT $4
	`, field, s.tableSource(), eventClause)

	rows, err := s.db.QueryContext(ctx, query, domain, from.UnixMicro(), to.UnixMicro(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return scanTopItems(rows)
}

func scanTopItems(rows *sql.Rows) ([]TopItem, error) {
	var result []TopItem
	for rows.Next() {
		var item TopItem
		if err := rows.Scan(&item.Name, &item.Count); err != nil {
			continue
		}
		result = append(result, item)
	}
	return result, nil
}

// EventItem for recent events
type EventItem struct {
	Name      string `json:"name"`
	URL       string `json:"url"`
	Pathname  string `json:"pathname"`
	Country   string `json:"country"`
	Browser   string `json:"browser"`
	OS        string `json:"os"`
	Device    string `json:"device"`
	Timestamp string `json:"timestamp"`
	Props     string `json:"props,omitempty"`
}

func (s *Store) GetRecentEvents(ctx context.Context, domain string, from, to time.Time, limit int) ([]EventItem, error) {
	if !s.ready {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	query := fmt.Sprintf(`
		SELECT
			name,
			COALESCE(url, '') as url,
			COALESCE(pathname, '') as pathname,
			COALESCE(country, 'Unknown') as country,
			COALESCE(browser, 'Unknown') as browser,
			COALESCE(os, 'Unknown') as os,
			COALESCE(device, 'desktop') as device,
			timestamp as ts,
			COALESCE(props, '') as props
		FROM %s
		WHERE domain = $1
		AND epoch_us(timestamp) >= $2
		AND epoch_us(timestamp) < $3
		ORDER BY timestamp DESC
		LIMIT $4
	`, s.tableSource())

	rows, err := s.db.QueryContext(ctx, query, domain, from.UnixMicro(), to.UnixMicro(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []EventItem
	for rows.Next() {
		var e EventItem
		var ts time.Time
		if err := rows.Scan(&e.Name, &e.URL, &e.Pathname, &e.Country, &e.Browser, &e.OS, &e.Device, &ts, &e.Props); err != nil {
			continue
		}
		e.Timestamp = ts.Format("2006-01-02 15:04:05")
		result = append(result, e)
	}
	return result, nil
}

func (s *Store) GetEventBreakdown(ctx context.Context, domain string, from, to time.Time) ([]TopItem, error) {
	return s.getTopBy(ctx, "name", "", domain, from, to, 10)
}

func (s *Store) GetUniquePages(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	return s.GetTopPages(ctx, domain, from, to, limit)
}

// Funnel types
type FunnelStep struct {
	Name    string  `json:"name"`
	Count   int64   `json:"count"`
	Percent float64 `json:"percent"`
}

type FunnelResult struct {
	Steps       []FunnelStep `json:"steps"`
	TotalStart  int64        `json:"total_start"`
	TotalFinish int64        `json:"total_finish"`
	Conversion  float64      `json:"conversion"`
}

func (s *Store) GetFunnel(ctx context.Context, domain string, from, to time.Time, steps []string) (*FunnelResult, error) {
	if !s.ready || len(steps) < 2 {
		return &FunnelResult{Steps: make([]FunnelStep, len(steps))}, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Simple funnel: count visitors who visited each page in sequence
	result := &FunnelResult{
		Steps: make([]FunnelStep, len(steps)),
	}

	for i, step := range steps {
		query := fmt.Sprintf(`
			SELECT COUNT(DISTINCT visitor_id)
			FROM %s
			WHERE domain = $1
			AND name = 'pageview'
			AND pathname = $2
			AND epoch_us(timestamp) >= $3
			AND epoch_us(timestamp) < $4
		`, s.tableSource())

		var count int64
		s.db.QueryRowContext(ctx, query, domain, step, from.UnixMicro(), to.UnixMicro()).Scan(&count)

		result.Steps[i] = FunnelStep{
			Name:  step,
			Count: count,
		}
	}

	if len(result.Steps) > 0 {
		result.TotalStart = result.Steps[0].Count
		result.TotalFinish = result.Steps[len(result.Steps)-1].Count

		for i := range result.Steps {
			if result.TotalStart > 0 {
				result.Steps[i].Percent = float64(result.Steps[i].Count) / float64(result.TotalStart) * 100
			}
		}

		if result.TotalStart > 0 {
			result.Conversion = float64(result.TotalFinish) / float64(result.TotalStart) * 100
		}
	}

	return result, nil
}

// FunnelStepDef for advanced funnel
type FunnelStepDef struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	Text  string `json:"text,omitempty"`
	Tag   string `json:"tag,omitempty"`
}

func (s *Store) GetFunnelAdvanced(ctx context.Context, domain string, from, to time.Time, steps []FunnelStepDef, windowMinutes int) (*FunnelResult, error) {
	// Simplified: just use basic funnel for pageviews
	var simpleSteps []string
	for _, step := range steps {
		if step.Type == "pageview" {
			simpleSteps = append(simpleSteps, step.Value)
		}
	}
	return s.GetFunnel(ctx, domain, from, to, simpleSteps)
}

// AutocaptureEvent type
type AutocaptureEvent struct {
	EventType string `json:"event_type"`
	Text      string `json:"text,omitempty"`
	Tag       string `json:"tag,omitempty"`
	Pathname  string `json:"pathname,omitempty"`
	Count     int64  `json:"count"`
}

func (s *Store) GetAutocaptureEvents(ctx context.Context, domain string, from, to time.Time, limit int) ([]AutocaptureEvent, error) {
	if !s.ready {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	query := fmt.Sprintf(`
		SELECT
			name as event_type,
			COALESCE(json_extract_string(props, '$.text'), '') as text,
			COALESCE(json_extract_string(props, '$.tag'), '') as tag,
			COALESCE(pathname, '') as pathname,
			COUNT(*) as count
		FROM %s
		WHERE domain = $1
		AND name IN ('click', 'submit', 'change')
		AND epoch_us(timestamp) >= $2
		AND epoch_us(timestamp) < $3
		GROUP BY name, json_extract_string(props, '$.text'), json_extract_string(props, '$.tag'), pathname
		ORDER BY count DESC
		LIMIT $4
	`, s.tableSource())

	rows, err := s.db.QueryContext(ctx, query, domain, from.UnixMicro(), to.UnixMicro(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []AutocaptureEvent
	for rows.Next() {
		var e AutocaptureEvent
		if err := rows.Scan(&e.EventType, &e.Text, &e.Tag, &e.Pathname, &e.Count); err != nil {
			continue
		}
		result = append(result, e)
	}
	return result, nil
}
