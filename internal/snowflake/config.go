package snowflake

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 保存当前进程的雪花发号配置，数据中心编号由 env.Config 提供。
type Config struct {
	// MachineID 是数据中心内的 7 位机器编号，不是组合后的 10 位节点编号。
	// 必须在 YAML 中显式填写，允许 0，不使用默认值或环境变量覆盖。
	MachineID int64 `yaml:"machine_id"`
}

// LoadConfig 按部署环境读取唯一配置文件，不创建发号器，也不回退其他环境。
// configDir 是直接包含 dev.yml、test.yml、prod.yml 的 snowflake 叶子目录。
// 文件必须是只含 machine_id 整数字段的单份非空 YAML 文档。
func LoadConfig(configDir, environment string) (Config, error) {
	switch environment {
	case "dev", "test", "prod":
	default:
		return Config{}, errors.New("unsupported snowflake deployment environment")
	}
	if strings.TrimSpace(configDir) == "" {
		return Config{}, errors.New("snowflake configuration directory is required")
	}
	filePath, err := filepath.Abs(filepath.Join(configDir, environment+".yml"))
	if err != nil {
		return Config{}, errors.New("cannot resolve snowflake configuration path")
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return Config{}, fmt.Errorf("cannot read snowflake configuration file %q", filePath)
	}
	return decodeSnowflakeConfig(data)
}

// decodeSnowflakeConfig 检查 YAML 标签，避免浮点数被库截断为整数。
// 按节点检查字段也能区分显式 0、遗漏和 null，并拒绝重复字段。
func decodeSnowflakeConfig(data []byte) (Config, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return Config{}, errors.New("snowflake configuration must contain one valid nonempty YAML document")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, errors.New("snowflake configuration must contain exactly one YAML document")
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode || document.Content[0].Tag != "!!map" {
		return Config{}, errors.New("snowflake configuration must be a YAML mapping containing machine_id")
	}
	mapping := document.Content[0]
	var machineNode *yaml.Node
	for i := 0; i < len(mapping.Content); i += 2 {
		key, value := mapping.Content[i], mapping.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || key.Value != "machine_id" {
			return Config{}, errors.New("unknown snowflake configuration field")
		}
		if machineNode != nil {
			return Config{}, errors.New("duplicate snowflake configuration field machine_id")
		}
		machineNode = value
	}
	if machineNode == nil {
		return Config{}, errors.New("snowflake configuration field machine_id is required")
	}
	if machineNode.Kind != yaml.ScalarNode || machineNode.Tag != "!!int" {
		return Config{}, errors.New("snowflake configuration field machine_id must be an integer between 0 and 127")
	}
	var machineID int64
	if err := machineNode.Decode(&machineID); err != nil || machineID < 0 || machineID > 127 {
		return Config{}, errors.New("snowflake configuration field machine_id must be an integer between 0 and 127")
	}
	return Config{MachineID: machineID}, nil
}
