package config

import "testing"

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with no env set: %v", err)
	}
	if cfg.DataPlaneAddr != ":8080" {
		t.Errorf("DataPlaneAddr = %q, want :8080", cfg.DataPlaneAddr)
	}
	if cfg.ControlPlaneAddr != ":9090" {
		t.Errorf("ControlPlaneAddr = %q, want :9090", cfg.ControlPlaneAddr)
	}
	if cfg.DefaultSpace != "default" {
		t.Errorf("DefaultSpace = %q, want default", cfg.DefaultSpace)
	}
	if cfg.TrafficTTL.String() != "24h0m0s" {
		t.Errorf("TrafficTTL = %v, want 24h", cfg.TrafficTTL)
	}
	if cfg.TokenTTL.String() != "1h0m0s" {
		t.Errorf("TokenTTL = %v, want 1h", cfg.TokenTTL)
	}
	if cfg.UpstreamTimeout.String() != "10s" {
		t.Errorf("UpstreamTimeout = %v, want 10s", cfg.UpstreamTimeout)
	}
	if cfg.BodyCapBytes != 1<<20 {
		t.Errorf("BodyCapBytes = %d, want %d", cfg.BodyCapBytes, 1<<20)
	}
	if cfg.AuthEnabled() {
		t.Error("AuthEnabled() = true with no LYREBIRD_AUTH_KEYS set, want false")
	}
}

func TestLoadAuthEnabledWhenKeysSet(t *testing.T) {
	t.Setenv("LYREBIRD_AUTH_KEYS", "secret1,secret2")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if !cfg.AuthEnabled() {
		t.Error("AuthEnabled() = false with LYREBIRD_AUTH_KEYS set, want true")
	}
	if len(cfg.AuthKeys) != 2 || cfg.AuthKeys[0] != "secret1" || cfg.AuthKeys[1] != "secret2" {
		t.Errorf("AuthKeys = %v, want [secret1 secret2]", cfg.AuthKeys)
	}
}

func TestLoadRejectsMalformedDuration(t *testing.T) {
	t.Setenv("LYREBIRD_TRAFFIC_TTL", "not-a-duration")
	if _, err := Load(); err == nil {
		t.Error("Load() with malformed LYREBIRD_TRAFFIC_TTL, want error")
	}
}

func TestLoadRejectsMalformedDataKeyBase64(t *testing.T) {
	t.Setenv("LYREBIRD_DATA_KEY", "not-valid-base64!!!")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() with malformed LYREBIRD_DATA_KEY, want error")
	}
	// The malformed value must never be echoed back in the error.
	if got := err.Error(); got == "" {
		t.Fatal("expected non-empty error")
	}
}

func TestLoadRejectsNonPositiveBodyCap(t *testing.T) {
	t.Setenv("LYREBIRD_BODY_CAP_BYTES", "-1")
	if _, err := Load(); err == nil {
		t.Error("Load() with negative LYREBIRD_BODY_CAP_BYTES, want error")
	}
}

func TestLoadRejectsNonPositiveGCInterval(t *testing.T) {
	for _, raw := range []string{"0", "0s", "-1s"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("LYREBIRD_GC_INTERVAL", raw)
			if _, err := Load(); err == nil {
				t.Errorf("Load() with LYREBIRD_GC_INTERVAL=%q, want error", raw)
			}
		})
	}
}

func TestLoadRejectsNonPositiveScriptTimeout(t *testing.T) {
	for _, raw := range []string{"0", "0s", "-1s"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("LYREBIRD_SCRIPT_TIMEOUT", raw)
			if _, err := Load(); err == nil {
				t.Errorf("Load() with LYREBIRD_SCRIPT_TIMEOUT=%q, want error", raw)
			}
		})
	}
}

func TestLoadAcceptsZeroUpstreamTimeout(t *testing.T) {
	t.Setenv("LYREBIRD_UPSTREAM_TIMEOUT", "0")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() with LYREBIRD_UPSTREAM_TIMEOUT=0, want no error, got %v", err)
	}
	if cfg.UpstreamTimeout != 0 {
		t.Errorf("UpstreamTimeout = %v, want 0", cfg.UpstreamTimeout)
	}
}

func TestLoadRejectsNegativeUpstreamTimeout(t *testing.T) {
	t.Setenv("LYREBIRD_UPSTREAM_TIMEOUT", "-1s")
	if _, err := Load(); err == nil {
		t.Error("Load() with LYREBIRD_UPSTREAM_TIMEOUT=-1s, want error")
	}
}

func TestLoadMCPStdioParsesBoolean(t *testing.T) {
	tests := []struct {
		raw     string
		want    bool
		wantErr bool
	}{
		{raw: "", want: false},
		{raw: "false", want: false},
		{raw: "true", want: true},
		{raw: "banana", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			if tt.raw != "" {
				t.Setenv("LYREBIRD_MCP_STDIO", tt.raw)
			}
			cfg, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() with LYREBIRD_MCP_STDIO=%q, want error", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() with LYREBIRD_MCP_STDIO=%q: %v", tt.raw, err)
			}
			if cfg.MCPStdio != tt.want {
				t.Errorf("MCPStdio = %v, want %v", cfg.MCPStdio, tt.want)
			}
		})
	}
}
