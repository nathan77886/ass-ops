CREATE INDEX IF NOT EXISTS idx_asset_status_snapshots_asset_collected
    ON asset_status_snapshots(asset_id, collected_at DESC);
