package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/viper"
)

// Config is a startup snapshot. Changes take effect after restarting the process.
type Config struct {
	AppName  string     `mapstructure:"app_name"`
	AppDebug bool       `mapstructure:"app_debug"`
	HTTP     HTTPConfig `mapstructure:"http"`
}

type HTTPConfig struct {
	Address           string        `mapstructure:"address"`
	BasePath          string        `mapstructure:"base_path"`
	ReadHeaderTimeout time.Duration `mapstructure:"read_header_timeout"`
	WriteTimeout      time.Duration `mapstructure:"write_timeout"`
	IdleTimeout       time.Duration `mapstructure:"idle_timeout"`
	ShutdownTimeout   time.Duration `mapstructure:"shutdown_timeout"`
	TrustedProxies    []string      `mapstructure:"trusted_proxies"`
}

func Load(path string) (Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	v.SetDefault("app_name", "go-service-template")
	v.SetDefault("app_debug", false)
	v.SetDefault("http.address", ":8080")
	v.SetDefault("http.base_path", "")
	v.SetDefault("http.read_header_timeout", "5s")
	v.SetDefault("http.write_timeout", "10s")
	v.SetDefault("http.idle_timeout", "60s")
	v.SetDefault("http.shutdown_timeout", "5s")
	v.SetDefault("http.trusted_proxies", []string{})
	if err := v.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("load application configuration: %w", err)
	}
	var cfg Config
	if err := v.UnmarshalExact(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode application configuration: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.AppName) == "" || strings.ContainsFunc(c.AppName, unicode.IsControl) {
		return fmt.Errorf("app_name must be nonempty and contain no control characters")
	}
	return c.HTTP.Validate()
}

func (c HTTPConfig) Validate() error {
	host, port, err := net.SplitHostPort(c.Address)
	if err != nil || c.Address != strings.TrimSpace(c.Address) {
		return fmt.Errorf("http.address must be a host:port listen address")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("http.address port must be between 1 and 65535")
	}
	if !validListenHost(host) {
		return fmt.Errorf("http.address contains an invalid host")
	}
	if c.BasePath != "" {
		if !strings.HasPrefix(c.BasePath, "/") || strings.HasSuffix(c.BasePath, "/") || strings.ContainsAny(c.BasePath, "?#%\\") || strings.ContainsFunc(c.BasePath, unicode.IsSpace) || strings.ContainsFunc(c.BasePath, unicode.IsControl) {
			return fmt.Errorf("http.base_path must be empty or an absolute path prefix without a trailing slash")
		}
		for _, part := range strings.Split(c.BasePath[1:], "/") {
			if part == "" || part == "." || part == ".." || strings.ContainsAny(part, ":*") {
				return fmt.Errorf("http.base_path must contain plain, nonempty path segments")
			}
		}
	}
	for _, timeout := range []struct {
		name  string
		value time.Duration
	}{
		{"read_header_timeout", c.ReadHeaderTimeout},
		{"write_timeout", c.WriteTimeout},
		{"idle_timeout", c.IdleTimeout},
		{"shutdown_timeout", c.ShutdownTimeout},
	} {
		if timeout.value <= 0 {
			return fmt.Errorf("http.%s must be a positive duration", timeout.name)
		}
	}
	for _, proxy := range c.TrustedProxies {
		if net.ParseIP(proxy) != nil {
			continue
		}
		if _, _, err := net.ParseCIDR(proxy); err != nil {
			return fmt.Errorf("http.trusted_proxies must contain only IP addresses or CIDR ranges")
		}
	}
	return nil
}

func validListenHost(host string) bool {
	if host == "" || net.ParseIP(host) != nil {
		return true
	}
	if strings.Contains(host, "%") {
		address, zone, ok := strings.Cut(host, "%")
		return ok && strings.Contains(address, ":") && zone != "" && net.ParseIP(address) != nil && !strings.ContainsAny(zone, "/\\%") && !strings.ContainsFunc(zone, unicode.IsSpace) && !strings.ContainsFunc(zone, unicode.IsControl)
	}
	if len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if character != '-' && (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}
