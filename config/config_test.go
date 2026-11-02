package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadStartupSnapshot(t *testing.T) {
	path := writeConfig(t, "app_name: test-service\nhttp:\n  address: '127.0.0.1:9090'\n  base_path: /api\n  write_timeout: 3s\n  trusted_proxies: ['127.0.0.1', '10.0.0.0/8']\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AppName != "test-service" || cfg.HTTP.Address != "127.0.0.1:9090" || cfg.HTTP.BasePath != "/api" || cfg.HTTP.WriteTimeout != 3*time.Second || cfg.HTTP.ReadHeaderTimeout != 5*time.Second || cfg.HTTP.IdleTimeout != time.Minute || cfg.HTTP.ShutdownTimeout != 5*time.Second || len(cfg.HTTP.TrustedProxies) != 2 {
		t.Fatalf("unexpected configuration: %+v", cfg)
	}
	if err := os.WriteFile(path, []byte("app_name: changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if cfg.AppName != "test-service" {
		t.Fatal("configuration changed after startup")
	}
	defaults, err := Load(writeConfig(t, "{}\n"))
	if err != nil || defaults.HTTP.Address != ":8080" || defaults.HTTP.BasePath != "" || len(defaults.HTTP.TrustedProxies) != 0 {
		t.Fatalf("unsafe or invalid defaults: %+v, %v", defaults, err)
	}
}

func TestLoadRejectsInvalidSettings(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field":      "http:\n  adress: ':8080'\n",
		"missing port":       "http:\n  address: localhost\n",
		"invalid port":       "http:\n  address: ':70000'\n",
		"invalid host":       "http:\n  address: 'bad host:8080'\n",
		"zero timeout":       "http:\n  shutdown_timeout: 0s\n",
		"invalid duration":   "http:\n  write_timeout: tomorrow\n",
		"relative prefix":    "http:\n  base_path: api\n",
		"trailing slash":     "http:\n  base_path: /api/\n",
		"path traversal":     "http:\n  base_path: /api/../v1\n",
		"router wildcard":    "http:\n  base_path: /:tenant\n",
		"encoded path":       "http:\n  base_path: /api%2fv1\n",
		"untrusted hostname": "http:\n  trusted_proxies: [localhost]\n",
		"empty app name":     "app_name: ' '\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, body)); err == nil {
				t.Fatal("invalid configuration was accepted")
			}
		})
	}
}

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "app.yml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
