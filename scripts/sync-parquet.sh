#!/bin/bash
# Sync parquet files from S3 to local storage
# Run via cron every 5 minutes

set -e

LOCAL_DIR="/data/parquet"
S3_BUCKET="s3://clickresearch-data-prod"

# Create local dir if not exists
mkdir -p "$LOCAL_DIR"

# Sync from S3 (only new/changed files)
s3cmd sync \
  --host=fra1.digitaloceanspaces.com \
  --host-bucket="%(bucket)s.fra1.digitaloceanspaces.com" \
  "$S3_BUCKET/" "$LOCAL_DIR/" \
  --exclude="*" \
  --include="*.parquet" \
  2>&1 | grep -v "^$"

echo "$(date): Sync completed"
