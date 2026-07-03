CREATE TABLE IF NOT EXISTS partitions (
    id          TEXT PRIMARY KEY,
    created_at  INTEGER NOT NULL,
    description TEXT
);

CREATE TABLE IF NOT EXISTS upstreams (
    id              TEXT PRIMARY KEY,
    "partition"     TEXT NOT NULL,
    match_host      TEXT NOT NULL,
    target_url      TEXT NOT NULL,
    tls_skip_verify INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_upstreams_partition ON upstreams("partition");

CREATE TABLE IF NOT EXISTS ephemeral_mocks (
    id            TEXT PRIMARY KEY,
    "partition"   TEXT NOT NULL,
    name          TEXT NOT NULL,
    priority      INTEGER NOT NULL DEFAULT 0,
    "group"       TEXT,
    created_at    INTEGER NOT NULL,
    expires_at    INTEGER,
    match_blob    BLOB NOT NULL,
    script_blob   BLOB,
    action_blob   BLOB NOT NULL,
    scenario_blob BLOB
);
CREATE INDEX IF NOT EXISTS idx_ephemeral_mocks_partition ON ephemeral_mocks("partition");
CREATE INDEX IF NOT EXISTS idx_ephemeral_mocks_expires
    ON ephemeral_mocks(expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE IF NOT EXISTS traffic (
    id              TEXT PRIMARY KEY,
    "partition"     TEXT NOT NULL,
    "timestamp"     INTEGER NOT NULL,
    method          TEXT NOT NULL,
    host            TEXT NOT NULL,
    path            TEXT NOT NULL,
    status          INTEGER NOT NULL,
    latency_ms      INTEGER NOT NULL,
    decision        TEXT NOT NULL,
    matched_mock_id TEXT,
    request_blob    BLOB,
    response_blob   BLOB
);
CREATE INDEX IF NOT EXISTS idx_traffic_partition_ts ON traffic("partition", "timestamp");
CREATE INDEX IF NOT EXISTS idx_traffic_timestamp     ON traffic("timestamp");
CREATE INDEX IF NOT EXISTS idx_traffic_status        ON traffic(status);
CREATE INDEX IF NOT EXISTS idx_traffic_host_path     ON traffic(host, path);

CREATE TABLE IF NOT EXISTS scenario_state (
    "partition" TEXT NOT NULL,
    mock_id     TEXT NOT NULL,
    idx         INTEGER NOT NULL DEFAULT 0,
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY ("partition", mock_id)
);
