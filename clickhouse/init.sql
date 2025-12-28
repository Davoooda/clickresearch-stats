-- ClickHouse init for ClickResearch
-- Local MergeTree table for fast queries, synced from S3

CREATE DATABASE IF NOT EXISTS analytics;

-- Main events table (matches S3 parquet schema - 16 columns)
CREATE TABLE IF NOT EXISTS analytics.events (
    domain LowCardinality(String),
    visitor_id String,
    name LowCardinality(String),
    url String DEFAULT '',
    pathname String DEFAULT '',
    referrer String DEFAULT '',
    timestamp DateTime64(6, 'UTC'),
    props String DEFAULT '{}',
    browser LowCardinality(String) DEFAULT '',
    browser_version LowCardinality(String) DEFAULT '',
    os LowCardinality(String) DEFAULT '',
    os_version LowCardinality(String) DEFAULT '',
    device LowCardinality(String) DEFAULT '',
    country LowCardinality(String) DEFAULT '',
    city LowCardinality(String) DEFAULT '',
    received_at DateTime64(6, 'UTC')
)
ENGINE = ReplacingMergeTree()
PARTITION BY toYYYYMM(timestamp)
ORDER BY (domain, timestamp, visitor_id, name, pathname)
TTL toDate(timestamp) + INTERVAL 1 YEAR
SETTINGS index_granularity = 8192;
