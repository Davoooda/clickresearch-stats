-- ClickHouse init for ClickResearch
-- Local MergeTree table for fast queries, synced from S3

CREATE DATABASE IF NOT EXISTS analytics;

-- Main events table (local cache)
CREATE TABLE IF NOT EXISTS analytics.events (
    -- Identification
    domain LowCardinality(String),
    visitor_id String,
    session_id String DEFAULT '',

    -- Event
    name LowCardinality(String),
    url String DEFAULT '',
    pathname String DEFAULT '',
    referrer String DEFAULT '',

    -- UTM
    utm_source LowCardinality(String) DEFAULT '',
    utm_medium LowCardinality(String) DEFAULT '',
    utm_campaign LowCardinality(String) DEFAULT '',
    utm_term String DEFAULT '',
    utm_content String DEFAULT '',

    -- Timestamps
    timestamp DateTime64(6, 'UTC'),
    received_at DateTime64(6, 'UTC'),

    -- User Agent
    browser LowCardinality(String) DEFAULT '',
    browser_version LowCardinality(String) DEFAULT '',
    os LowCardinality(String) DEFAULT '',
    os_version LowCardinality(String) DEFAULT '',
    device LowCardinality(String) DEFAULT '',

    -- Geo
    country LowCardinality(String) DEFAULT '',
    city LowCardinality(String) DEFAULT '',

    -- Custom props
    props String DEFAULT '{}'
)
ENGINE = ReplacingMergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (domain, timestamp, visitor_id, name, pathname)
TTL timestamp + INTERVAL 1 YEAR
SETTINGS index_granularity = 8192;

-- Index for pathname filtering
ALTER TABLE analytics.events ADD INDEX IF NOT EXISTS idx_pathname pathname TYPE bloom_filter GRANULARITY 4;

-- Index for visitor_id lookups
ALTER TABLE analytics.events ADD INDEX IF NOT EXISTS idx_visitor visitor_id TYPE bloom_filter GRANULARITY 4;
