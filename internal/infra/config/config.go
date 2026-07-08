// Package config loads Lyrebird's runtime configuration from LYREBIRD_*
// environment variables. Every setting has a frictionless default; security
// features (control-plane auth, a stable at-rest key) only activate when the
// operator explicitly sets the relevant variable (constitution Principle V).
package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime settings resolved from the environment.
type Config struct {
	DataPlaneAddr    string
	ControlPlaneAddr string
	// GRPCPlaneAddr is the plaintext-gRPC (h2c) data-plane listen address,
	// from LYREBIRD_GRPC_PORT. It is opt-in: empty means no gRPC listener is
	// bound and Lyrebird behaves exactly as it does without the feature
	// (constitution Principle V — a new surface only appears when explicitly
	// configured). The gRPC data plane, like the HTTP data plane, is never
	// authenticated (FR-011).
	GRPCPlaneAddr   string
	TrafficTTL      time.Duration
	DefaultSpace    string
	AllowProxyHosts []string
	AuthKeys        []string
	TokenTTL        time.Duration
	// DataKeyB64 is the raw, still-encoded LYREBIRD_DATA_KEY value, if any.
	// Decoding into an actual key is internal/infra/crypto's job.
	DataKeyB64      string
	BodyCapBytes    int64
	DBPath          string
	SeedDir         string
	GCInterval      time.Duration
	UpstreamTimeout time.Duration
	// ScriptTimeout bounds a mock's sandboxed match_src/respond_src script
	// execution (FR-016) — a misbehaving script is interrupted and treated
	// as a fail-safe failure, never left to hang.
	ScriptTimeout time.Duration
	// MCPStdio, when true, makes the process serve MCP over stdin/stdout
	// only (no HTTP listeners) instead of running the normal HTTP daemon —
	// the local-agent transport mode (contracts/mcp-tools.md).
	MCPStdio bool
	// MITMEnabled activates the transparent forward-proxy/MITM data-plane
	// path (T054) — off by default (Principle V), on via
	// LYREBIRD_MITM_ENABLED.
	MITMEnabled bool
	// MITMCACertFile/MITMCAKeyFile point at mounted PEM files for a stable
	// MITM CA (data-model.md's MITM Certificate Authority section). Both
	// empty means a fresh, disposable CA is generated every boot; both must
	// be set together, or neither.
	MITMCACertFile string
	MITMCAKeyFile  string
}

// Load reads and validates configuration from the environment. It fails fast
// on malformed values but never echoes secret values (LYREBIRD_DATA_KEY,
// LYREBIRD_AUTH_KEYS) in any error message.
func Load() (Config, error) {
	cfg := Config{
		DataPlaneAddr:    ":" + getenv("LYREBIRD_DATA_PORT", "8080"),
		ControlPlaneAddr: ":" + getenv("LYREBIRD_CONTROL_PORT", "9090"),
		DefaultSpace:     getenv("LYREBIRD_DEFAULT_SPACE", "default"),
		DataKeyB64:       os.Getenv("LYREBIRD_DATA_KEY"),
		DBPath:           getenv("LYREBIRD_DB_PATH", "/data/lyrebird.db"),
		SeedDir:          getenv("LYREBIRD_SEED_DIR", "/config"),
	}

	// Opt-in: only set an address when a port is given, so an unset
	// LYREBIRD_GRPC_PORT leaves GRPCPlaneAddr empty and bootstrap binds no
	// gRPC listener at all.
	if port := os.Getenv("LYREBIRD_GRPC_PORT"); port != "" {
		cfg.GRPCPlaneAddr = ":" + port
	}

	var err error
	if cfg.TrafficTTL, err = parseNonNegativeDuration("LYREBIRD_TRAFFIC_TTL", "24h"); err != nil {
		return Config{}, err
	}
	if cfg.TokenTTL, err = parseNonNegativeDuration("LYREBIRD_TOKEN_TTL", "1h"); err != nil {
		return Config{}, err
	}
	if cfg.GCInterval, err = parsePositiveDuration("LYREBIRD_GC_INTERVAL", "1m"); err != nil {
		return Config{}, err
	}
	if cfg.UpstreamTimeout, err = parseNonNegativeDuration("LYREBIRD_UPSTREAM_TIMEOUT", "10s"); err != nil {
		return Config{}, err
	}
	if cfg.ScriptTimeout, err = parsePositiveDuration("LYREBIRD_SCRIPT_TIMEOUT", "100ms"); err != nil {
		return Config{}, err
	}
	if cfg.BodyCapBytes, err = parsePositiveInt64("LYREBIRD_BODY_CAP_BYTES", 1<<20); err != nil {
		return Config{}, err
	}
	cfg.AllowProxyHosts = parseCSV(os.Getenv("LYREBIRD_ALLOW_PROXY_HOSTS"))
	cfg.AuthKeys = parseCSV(os.Getenv("LYREBIRD_AUTH_KEYS"))
	if raw := os.Getenv("LYREBIRD_MCP_STDIO"); raw != "" {
		mcpStdio, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("config: LYREBIRD_MCP_STDIO=%q is not a valid boolean", raw)
		}
		cfg.MCPStdio = mcpStdio
	}

	if cfg.DataKeyB64 != "" {
		if _, err := base64.StdEncoding.DecodeString(cfg.DataKeyB64); err != nil {
			return Config{}, fmt.Errorf("config: LYREBIRD_DATA_KEY is not valid base64")
		}
	}

	if raw := os.Getenv("LYREBIRD_MITM_ENABLED"); raw != "" {
		mitmEnabled, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("config: LYREBIRD_MITM_ENABLED=%q is not a valid boolean", raw)
		}
		cfg.MITMEnabled = mitmEnabled
	}
	cfg.MITMCACertFile = os.Getenv("LYREBIRD_MITM_CA_CERT_FILE")
	cfg.MITMCAKeyFile = os.Getenv("LYREBIRD_MITM_CA_KEY_FILE")
	if (cfg.MITMCACertFile == "") != (cfg.MITMCAKeyFile == "") {
		return Config{}, fmt.Errorf("config: LYREBIRD_MITM_CA_CERT_FILE and LYREBIRD_MITM_CA_KEY_FILE must both be set, or neither")
	}

	return cfg, nil
}

// AuthEnabled reports whether control-plane authentication is active
// (Principle V: activates only when the operator sets LYREBIRD_AUTH_KEYS).
func (c Config) AuthEnabled() bool {
	return len(c.AuthKeys) > 0
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parsePositiveDuration(key, def string) (time.Duration, error) {
	raw := getenv(key, def)
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("config: %s=%q must be a positive duration", key, raw)
	}
	return d, nil
}

func parseNonNegativeDuration(key, def string) (time.Duration, error) {
	raw := getenv(key, def)
	d, err := time.ParseDuration(raw)
	if err != nil || d < 0 {
		return 0, fmt.Errorf("config: %s=%q must be a non-negative duration", key, raw)
	}
	return d, nil
}

func parsePositiveInt64(key string, def int64) (int64, error) {
	raw := os.Getenv(key)
	if raw == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("config: %s=%q is not a positive integer", key, raw)
	}
	return n, nil
}

func parseCSV(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
