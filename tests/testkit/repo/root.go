// Package repo 提供与具体业务模块名解耦的 Go 模块仓库根目录定位工具。
package repo

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// FindRoot 解析仓库根目录绝对路径。
// explicit 非空时按当前工作目录将其解析为绝对路径返回（目录是否存在交由上层使用方按需校验）；
// explicit 为空时从当前进程的工作目录逐级向上查找包含有效 go.mod 的最近目录。
func FindRoot(explicit string) (string, error) {
	if explicit != "" {
		absolute, err := filepath.Abs(explicit)
		if err != nil {
			return "", fmt.Errorf("resolve repository root: %w", err)
		}
		return absolute, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("determine working directory: %w", err)
	}
	return FindRootFromDir(cwd)
}

// FindRootFromDir 从 startDir 开始逐级向上查找最近一个包含合法 module 声明的 go.mod 所在目录。
func FindRootFromDir(startDir string) (string, error) {
	if strings.TrimSpace(startDir) == "" {
		return "", errors.New("start directory is required")
	}
	current, err := filepath.Abs(startDir)
	if err != nil {
		return "", fmt.Errorf("resolve start directory: %w", err)
	}
	for {
		if hasGoModuleFile(filepath.Join(current, "go.mod")) {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("cannot locate go.mod repository root; provide an explicit root path")
		}
		current = parent
	}
}

// hasGoModuleFile 检查指定路径是否为普通文件且包含合法的 Go module 声明行，不绑定任何具体项目名。
func hasGoModuleFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "module"); ok {
			// module 关键字之后必须紧跟空白及非空模块路径（如 "module example.com/foo"）。
			if len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') && strings.TrimSpace(rest) != "" {
				return true
			}
		}
		return false
	}
	return false
}
