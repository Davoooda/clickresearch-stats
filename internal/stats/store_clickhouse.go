package stats

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

type ClickHouseStore struct {
	conn       driver.Conn
	s3Path     string
	s3Key      string
	s3Secret   string
	stopCh     chan struct{}
	lastSync   time.Time
	syncMu     sync.Mutex
}

type ClickHouseConfig struct {
	Addr      string // e.g., "localhost:9000"
	Database  string // e.g., "analytics"
	S3Endpoint string
	S3Key     string
	S3Secret  string
	S3Bucket  string
	S3Prefix  string
}

func NewClickHouseStore(cfg ClickHouseConfig) (*ClickHouseStore, error) {
	conn, err := clickhouse.Open(&clickhouse.Options{
		Addr: []string{cfg.Addr},
		Auth: clickhouse.Auth{
			Database: cfg.Database,
		},
		Settings: clickhouse.Settings{
			"max_execution_time": 300,
		},
		Compression: &clickhouse.Compression{
			Method: clickhouse.CompressionLZ4,
		},
		DialTimeout:     5 * time.Second,
		MaxOpenConns:    10,
		MaxIdleConns:    5,
		ConnMaxLifetime: time.Hour,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to clickhouse: %w", err)
	}

	if err := conn.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ping clickhouse: %w", err)
	}

	s3Path := fmt.Sprintf("https://%s/%s/%s**/*.parquet",
		cfg.S3Endpoint, cfg.S3Bucket, cfg.S3Prefix)

	log.Printf("ClickHouse: connected, syncing from %s", s3Path)

	store := &ClickHouseStore{
		conn:     conn,
		s3Path:   s3Path,
		s3Key:    cfg.S3Key,
		s3Secret: cfg.S3Secret,
		stopCh:   make(chan struct{}),
	}

	// Create local table if not exists
	if err := store.ensureTable(); err != nil {
		return nil, fmt.Errorf("failed to create events table: %w", err)
	}

	// Initial sync from S3
	if err := store.syncFromS3(); err != nil {
		log.Printf("Warning: initial S3 sync failed: %v", err)
	}

	// Start background refresh every 5 minutes
	go store.refreshLoop()

	return store, nil
}

func (s *ClickHouseStore) Close() error {
	close(s.stopCh)
	return s.conn.Close()
}

func (s *ClickHouseStore) ensureTable() error {
	ctx := context.Background()

	createTable := `
		CREATE TABLE IF NOT EXISTS events (
			domain LowCardinality(String),
			visitor_id String,
			session_id String DEFAULT '',
			name LowCardinality(String),
			url String DEFAULT '',
			pathname String DEFAULT '',
			referrer String DEFAULT '',
			utm_source LowCardinality(String) DEFAULT '',
			utm_medium LowCardinality(String) DEFAULT '',
			utm_campaign LowCardinality(String) DEFAULT '',
			utm_term String DEFAULT '',
			utm_content String DEFAULT '',
			timestamp DateTime64(6, 'UTC'),
			received_at DateTime64(6, 'UTC'),
			browser LowCardinality(String) DEFAULT '',
			browser_version LowCardinality(String) DEFAULT '',
			os LowCardinality(String) DEFAULT '',
			os_version LowCardinality(String) DEFAULT '',
			device LowCardinality(String) DEFAULT '',
			country LowCardinality(String) DEFAULT '',
			city LowCardinality(String) DEFAULT '',
			props String DEFAULT '{}'
		)
		ENGINE = ReplacingMergeTree()
		PARTITION BY toYYYYMM(timestamp)
		ORDER BY (domain, timestamp, visitor_id, name, pathname)
		TTL toDate(timestamp) + INTERVAL 1 YEAR
		SETTINGS index_granularity = 8192
	`
	return s.conn.Exec(ctx, createTable)
}

func (s *ClickHouseStore) syncFromS3() error {
	s.syncMu.Lock()
	defer s.syncMu.Unlock()

	ctx := context.Background()
	start := time.Now()

	// Truncate and reload from S3 (simple approach for now)
	// In production, could do incremental sync based on received_at
	if err := s.conn.Exec(ctx, "TRUNCATE TABLE events"); err != nil {
		return fmt.Errorf("truncate failed: %w", err)
	}

	insertQuery := fmt.Sprintf(`
		INSERT INTO events
		SELECT * FROM s3('%s', '%s', '%s', 'Parquet')
	`, s.s3Path, s.s3Key, s.s3Secret)

	if err := s.conn.Exec(ctx, insertQuery); err != nil {
		return fmt.Errorf("insert from s3 failed: %w", err)
	}

	// Get row count
	var count uint64
	s.conn.QueryRow(ctx, "SELECT count() FROM events").Scan(&count)

	s.lastSync = time.Now()
	log.Printf("ClickHouse: synced %d events from S3 in %v", count, time.Since(start))

	return nil
}

func (s *ClickHouseStore) refreshLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopCh:
			log.Println("ClickHouse: refresh loop stopped")
			return
		case <-ticker.C:
			if err := s.syncFromS3(); err != nil {
				log.Printf("ClickHouse: sync error: %v", err)
			}
		}
	}
}

func (s *ClickHouseStore) s3Source() string {
	// Now read from local table instead of S3
	return "events"
}

// Overview stats
func (s *ClickHouseStore) GetOverview(ctx context.Context, domain string, from, to time.Time) (*Overview, error) {
	query := fmt.Sprintf(`
		SELECT
			countIf(name = 'pageview') as pageviews,
			uniq(visitor_id) as unique_visitors,
			count() as events
		FROM %s
		WHERE domain = ?
		AND timestamp >= ?
		AND timestamp < ?
	`, s.s3Source())

	var pageviews, uniqueVisitors, events uint64
	row := s.conn.QueryRow(ctx, query, domain, from, to)
	if err := row.Scan(&pageviews, &uniqueVisitors, &events); err != nil {
		return nil, err
	}
	return &Overview{
		Pageviews:      int64(pageviews),
		UniqueVisitors: int64(uniqueVisitors),
		Events:         int64(events),
	}, nil
}

// Time series for charts
func (s *ClickHouseStore) GetPageviewsTimeSeries(ctx context.Context, domain string, from, to time.Time, interval string) ([]TimeSeriesPoint, error) {
	dateFunc := "toStartOfDay(timestamp)"
	if interval == "hour" {
		dateFunc = "toStartOfHour(timestamp)"
	}

	query := fmt.Sprintf(`
		SELECT
			%s as time_bucket,
			count() as count
		FROM %s
		WHERE domain = ?
		AND name = 'pageview'
		AND timestamp >= ?
		AND timestamp < ?
		GROUP BY time_bucket
		ORDER BY time_bucket
	`, dateFunc, s.s3Source())

	rows, err := s.conn.Query(ctx, query, domain, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []TimeSeriesPoint
	for rows.Next() {
		var t time.Time
		var count uint64
		if err := rows.Scan(&t, &count); err != nil {
			continue
		}
		format := "2006-01-02"
		if interval == "hour" {
			format = "2006-01-02T15:00"
		}
		result = append(result, TimeSeriesPoint{Time: t.Format(format), Value: int64(count)})
	}
	return result, nil
}

// Top pages
func (s *ClickHouseStore) GetTopPages(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	return s.getTopBy(ctx, "pathname", "pageview", domain, from, to, limit)
}

// Top sources (referrers)
func (s *ClickHouseStore) GetTopSources(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	query := fmt.Sprintf(`
		SELECT
			multiIf(
				referrer = '' OR referrer IS NULL, 'Direct',
				position(referrer, ?) > 0, 'Direct',
				domain(referrer)
			) as source,
			count() as count
		FROM %s
		WHERE domain = ?
		AND name = 'pageview'
		AND timestamp >= ?
		AND timestamp < ?
		GROUP BY source
		ORDER BY count DESC
		LIMIT ?
	`, s.s3Source())

	rows, err := s.conn.Query(ctx, query, domain, domain, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanTopItems(rows)
}

// Top browsers
func (s *ClickHouseStore) GetTopBrowsers(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	return s.getTopBy(ctx, "browser", "", domain, from, to, limit)
}

// Top countries
func (s *ClickHouseStore) GetTopCountries(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	return s.getTopBy(ctx, "country", "", domain, from, to, limit)
}

// Top devices
func (s *ClickHouseStore) GetTopDevices(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	return s.getTopBy(ctx, "device", "", domain, from, to, limit)
}

// UTM stats
func (s *ClickHouseStore) GetTopUTMSources(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	return s.getTopByNonEmpty(ctx, "utm_source", "pageview", domain, from, to, limit)
}

func (s *ClickHouseStore) GetTopUTMMediums(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	return s.getTopByNonEmpty(ctx, "utm_medium", "pageview", domain, from, to, limit)
}

func (s *ClickHouseStore) GetTopUTMCampaigns(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	return s.getTopByNonEmpty(ctx, "utm_campaign", "pageview", domain, from, to, limit)
}

func (s *ClickHouseStore) getTopByNonEmpty(ctx context.Context, field, eventFilter, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	eventClause := ""
	if eventFilter != "" {
		eventClause = fmt.Sprintf("AND name = '%s'", eventFilter)
	}

	query := fmt.Sprintf(`
		SELECT
			%s as name,
			count() as count
		FROM %s
		WHERE domain = ?
		%s
		AND %s IS NOT NULL AND %s != ''
		AND timestamp >= ?
		AND timestamp < ?
		GROUP BY name
		ORDER BY count DESC
		LIMIT ?
	`, field, s.s3Source(), eventClause, field, field)

	rows, err := s.conn.Query(ctx, query, domain, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanTopItems(rows)
}

func (s *ClickHouseStore) getTopBy(ctx context.Context, field, eventFilter, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	eventClause := ""
	if eventFilter != "" {
		eventClause = fmt.Sprintf("AND name = '%s'", eventFilter)
	}

	query := fmt.Sprintf(`
		SELECT
			if(%s = '' OR %s IS NULL, 'Unknown', %s) as name,
			count() as count
		FROM %s
		WHERE domain = ?
		%s
		AND timestamp >= ?
		AND timestamp < ?
		GROUP BY name
		ORDER BY count DESC
		LIMIT ?
	`, field, field, field, s.s3Source(), eventClause)

	rows, err := s.conn.Query(ctx, query, domain, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	return s.scanTopItems(rows)
}

func (s *ClickHouseStore) scanTopItems(rows driver.Rows) ([]TopItem, error) {
	var result []TopItem
	for rows.Next() {
		var item TopItem
		var count uint64
		if err := rows.Scan(&item.Name, &count); err != nil {
			continue
		}
		item.Count = int64(count)
		result = append(result, item)
	}
	return result, nil
}

// Recent events
func (s *ClickHouseStore) GetRecentEvents(ctx context.Context, domain string, from, to time.Time, limit int) ([]EventItem, error) {
	query := fmt.Sprintf(`
		SELECT
			name,
			ifNull(url, '') as url,
			ifNull(pathname, '') as pathname,
			if(country = '' OR country IS NULL, 'Unknown', country) as country,
			if(browser = '' OR browser IS NULL, 'Unknown', browser) as browser,
			if(os = '' OR os IS NULL, 'Unknown', os) as os,
			if(device = '' OR device IS NULL, 'desktop', device) as device,
			timestamp as ts,
			ifNull(props, '') as props
		FROM %s
		WHERE domain = ?
		AND timestamp >= ?
		AND timestamp < ?
		ORDER BY timestamp DESC
		LIMIT ?
	`, s.s3Source())

	rows, err := s.conn.Query(ctx, query, domain, from, to, limit)
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

// Event breakdown
func (s *ClickHouseStore) GetEventBreakdown(ctx context.Context, domain string, from, to time.Time) ([]TopItem, error) {
	return s.getTopBy(ctx, "name", "", domain, from, to, 10)
}

// Unique pages
func (s *ClickHouseStore) GetUniquePages(ctx context.Context, domain string, from, to time.Time, limit int) ([]TopItem, error) {
	return s.GetTopPages(ctx, domain, from, to, limit)
}

// Funnel analysis
func (s *ClickHouseStore) GetFunnel(ctx context.Context, domain string, from, to time.Time, steps []string) (*FunnelResult, error) {
	if len(steps) < 2 {
		return &FunnelResult{Steps: make([]FunnelStep, len(steps))}, nil
	}

	result := &FunnelResult{
		Steps: make([]FunnelStep, len(steps)),
	}

	for i, step := range steps {
		query := fmt.Sprintf(`
			SELECT uniq(visitor_id)
			FROM %s
			WHERE domain = ?
			AND name = 'pageview'
			AND pathname = ?
			AND timestamp >= ?
			AND timestamp < ?
		`, s.s3Source())

		var count uint64
		s.conn.QueryRow(ctx, query, domain, step, from, to).Scan(&count)

		result.Steps[i] = FunnelStep{
			Name:  step,
			Count: int64(count),
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

// Advanced funnel
func (s *ClickHouseStore) GetFunnelAdvanced(ctx context.Context, domain string, from, to time.Time, steps []FunnelStepDef, windowMinutes int) (*FunnelResult, error) {
	var simpleSteps []string
	for _, step := range steps {
		if step.Type == "pageview" {
			simpleSteps = append(simpleSteps, step.Value)
		}
	}
	return s.GetFunnel(ctx, domain, from, to, simpleSteps)
}

// Autocapture events
func (s *ClickHouseStore) GetAutocaptureEvents(ctx context.Context, domain string, from, to time.Time, limit int) ([]AutocaptureEvent, error) {
	query := fmt.Sprintf(`
		SELECT
			name as event_type,
			ifNull(simpleJSONExtractString(props, 'text'), '') as text,
			ifNull(simpleJSONExtractString(props, 'tag'), '') as tag,
			ifNull(pathname, '') as pathname,
			count() as count
		FROM %s
		WHERE domain = ?
		AND name IN ('click', 'submit', 'change')
		AND timestamp >= ?
		AND timestamp < ?
		GROUP BY name, text, tag, pathname
		ORDER BY count DESC
		LIMIT ?
	`, s.s3Source())

	rows, err := s.conn.Query(ctx, query, domain, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []AutocaptureEvent
	for rows.Next() {
		var e AutocaptureEvent
		var count uint64
		if err := rows.Scan(&e.EventType, &e.Text, &e.Tag, &e.Pathname, &count); err != nil {
			continue
		}
		e.Count = int64(count)
		result = append(result, e)
	}
	return result, nil
}
