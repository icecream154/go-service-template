//go:build integration || business

package harness

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"go-service-template/app/integrations/company/base"
	authnjwt "go-service-template/internal/authn/jwt"
	"go-service-template/tests/testkit/httpstub"

	"gopkg.in/yaml.v3"
)

type loggerYAML struct {
	ServiceName string `yaml:"service_name"`
	MinLevel    string `yaml:"min_level"`
}

type jwtYAML struct {
	Issuer     string `yaml:"issuer"`
	Audience   string `yaml:"audience"`
	SigningKey string `yaml:"signing_key"`
	TokenTTL   string `yaml:"token_ttl"`
}

type snowflakeYAML struct {
	MachineID int64 `yaml:"machine_id"`
}

// discoverAndCreateCompanyStubs 读取仓库正式 config/integrations/company/test.yml 的服务拓扑骨架，
// 动态为其中声明的每个公司内部服务创建独立的 httpstub.Server 并生成替换了地址与测试超时的配置映射。
// 后续仓库新增任何公司内部服务或 Controller 时，无需改动测试装配器代码即可自动获得默认拦截桩。
func discoverAndCreateCompanyStubs(repoRoot string, connectTimeout, requestTimeout time.Duration) (map[string]*httpstub.Server, map[string]base.ServiceConfig, error) {
	templatePath := filepath.Join(repoRoot, "config", "integrations", "company", "test.yml")
	raw, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, nil, fmt.Errorf("read company config skeleton %s: %w", templatePath, err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	var parsed map[string]base.ServiceConfig
	if err := decoder.Decode(&parsed); err != nil {
		return nil, nil, fmt.Errorf("decode company config skeleton: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, nil, errors.New("company config skeleton must contain a single YAML document")
	}

	stubs := make(map[string]*httpstub.Server, len(parsed))
	rewritten := make(map[string]base.ServiceConfig, len(parsed))

	for _, serviceName := range slices.Sorted(maps.Keys(parsed)) {
		stub := httpstub.NewServer()
		stubs[serviceName] = stub

		svc := parsed[serviceName]
		svc.BaseURL = stub.URL()
		svc.ConnectTimeout = connectTimeout

		rewrittenDomains := make(map[string]map[string]base.ControllerConfig, len(svc.Domains))
		for domainName, controllers := range svc.Domains {
			rewrittenControllers := make(map[string]base.ControllerConfig, len(controllers))
			for controllerName := range controllers {
				rewrittenControllers[controllerName] = base.ControllerConfig{
					RequestTimeout: requestTimeout,
				}
			}
			rewrittenDomains[domainName] = rewrittenControllers
		}
		svc.Domains = rewrittenDomains
		rewritten[serviceName] = svc
	}
	return stubs, rewritten, nil
}

// writeApplicationTestConfigs 在已包含 database/mysql/test.yml 的临时配置根目录下写入其余组件所需的 test.yml。
func writeApplicationTestConfigs(configDir string, jwtCfg authnjwt.Config, machineID int64, companyCfg map[string]base.ServiceConfig) error {
	files := map[string]any{
		"logger/test.yml": loggerYAML{
			ServiceName: "go-service-template",
			MinLevel:    "INFO",
		},
		"authn/jwt/test.yml": jwtYAML{
			Issuer:     jwtCfg.Issuer,
			Audience:   jwtCfg.Audience,
			SigningKey: jwtCfg.SigningKey,
			TokenTTL:   jwtCfg.TokenTTL.String(),
		},
		"snowflake/test.yml": snowflakeYAML{
			MachineID: machineID,
		},
		"integrations/company/test.yml": companyCfg,
	}

	for relPath, payload := range files {
		encoded, err := yaml.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal %s: %w", relPath, err)
		}
		target := filepath.Join(configDir, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("create config directory for %s: %w", relPath, err)
		}
		if err := os.WriteFile(target, encoded, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", relPath, err)
		}
	}
	return nil
}
