package company

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"go-service-template/app/integrations/company/base"

	"gopkg.in/yaml.v3"
)

// configuration 按目标服务名读取配置，新增服务不需要修改 YAML 加载结构。
type configuration map[string]serviceConfig

type serviceConfig struct {
	BaseURL        string                             `yaml:"base_url"`
	ConnectTimeout time.Duration                      `yaml:"connect_timeout"`
	Domains        map[string]map[string]clientConfig `yaml:",inline"`
}

type clientConfig struct {
	RequestTimeout time.Duration `yaml:"request_timeout"`
}

// loadConfig 接收直接包含 dev/test/prod.yml 的叶子目录，不回退其他环境文件。
func loadConfig(configDir, environment string) (configuration, error) {
	switch environment {
	case "dev", "test", "prod":
	default:
		return nil, errors.New("unsupported company HTTP client deployment environment")
	}
	if strings.TrimSpace(configDir) == "" {
		return nil, errors.New("company HTTP client configuration directory is required")
	}
	filePath, err := filepath.Abs(filepath.Join(configDir, environment+".yml"))
	if err != nil {
		return nil, errors.New("cannot resolve company HTTP client configuration path")
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("cannot read company HTTP client configuration file %q: %w", filePath, err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var config configuration
	if err := decoder.Decode(&config); err != nil {
		return nil, errors.New("invalid company HTTP client configuration YAML: check field names and scalar types")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, errors.New("company HTTP client configuration must contain exactly one YAML document")
	}
	if len(config) == 0 {
		return nil, errors.New("company HTTP client configuration must not be empty")
	}
	for _, serviceName := range slices.Sorted(maps.Keys(config)) {
		service := config[serviceName]
		if serviceName == "" || strings.TrimSpace(serviceName) != serviceName {
			return nil, errors.New("company HTTP client service name must be nonempty without surrounding whitespace")
		}
		service.BaseURL, err = resolveBaseURL(service.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("invalid %s.base_url: %w", serviceName, err)
		}
		if service.ConnectTimeout <= 0 {
			return nil, fmt.Errorf("%s.connect_timeout must be positive", serviceName)
		}
		if len(service.Domains) == 0 {
			return nil, fmt.Errorf("%s must configure at least one domain", serviceName)
		}
		for _, domain := range slices.Sorted(maps.Keys(service.Domains)) {
			controllers := service.Domains[domain]
			if domain == "" || strings.TrimSpace(domain) != domain || len(controllers) == 0 {
				return nil, fmt.Errorf("invalid domain configuration in %s", serviceName)
			}
			for _, controller := range slices.Sorted(maps.Keys(controllers)) {
				if controller == "" || strings.TrimSpace(controller) != controller {
					return nil, fmt.Errorf("invalid controller name in %s.%s", serviceName, domain)
				}
				client := base.Config{BaseURL: service.BaseURL, RequestTimeout: controllers[controller].RequestTimeout}
				if err := client.Validate(); err != nil {
					return nil, fmt.Errorf("invalid %s.%s.%s configuration: %w", serviceName, domain, controller, err)
				}
			}
		}
		config[serviceName] = service
	}
	return config, nil
}

var baseURLVariable = regexp.MustCompile(`^\$\{([A-Za-z_][A-Za-z0-9_]*)\}$`)

// resolveBaseURL 仅展开完整 ${NAME} 引用，不回显值，也不递归展开。
func resolveBaseURL(value string) (string, error) {
	match := baseURLVariable.FindStringSubmatch(value)
	if match == nil {
		if strings.Contains(value, "${") {
			return "", errors.New("environment reference must be a complete ${NAME} value")
		}
		return value, nil
	}
	resolved, ok := os.LookupEnv(match[1])
	if !ok || resolved == "" {
		return "", fmt.Errorf("environment variable %s is missing or empty", match[1])
	}
	return resolved, nil
}
