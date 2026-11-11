package format

import (
	"strings"
	"unicode/utf8"

	"go-service-template/internal/check"
)

// PathStyle 指定词法规则，不依赖运行程序的操作系统。
type PathStyle uint8

const (
	PathPOSIX PathStyle = iota
	PathWindows
)

// PathText 检查非空、有效 UTF-8、无 NUL 的路径文本，不访问文件系统。
// POSIX 文件系统也可能保存非 UTF-8 文件名，本方法明确只接受文本路径。
func PathText(value string) error {
	if value == "" || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return check.NewViolation("path.text", "must be nonempty UTF-8 text without NUL")
	}
	return nil
}

// FileName 检查单个文件名片段，拒绝分隔符、点目录；Windows 额外拒绝设备名及保留字符。
// 检查不证明文件存在或可以安全访问，也不裁剪空格或改写大小写。
func FileName(value string, style PathStyle) error {
	if !pathStyleValid(style) {
		return check.NewRuleError("path.style", "unknown path style")
	}
	if err := PathText(value); err != nil {
		return err
	}
	if value == "." || value == ".." || strings.ContainsRune(value, '/') || style == PathWindows && strings.ContainsRune(value, '\\') {
		return check.NewViolation("path.filename", "must be a single filename component")
	}
	if style == PathWindows {
		if strings.ContainsAny(value, `<>:"|?*`) || strings.HasSuffix(value, " ") || strings.HasSuffix(value, ".") {
			return check.NewViolation("path.filename", "must not use reserved Windows characters or trailing spaces or dots")
		}
		if strings.ContainsFunc(value, func(r rune) bool { return r < 32 }) {
			return check.NewViolation("path.filename", "must not contain Windows control characters")
		}
		stem, _, _ := strings.Cut(value, ".")
		stem = strings.TrimRight(stem, " ")
		stem = strings.ToUpper(stem)
		if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CONIN$" || stem == "CONOUT$" || pathWindowsDevice(stem) {
			return check.NewViolation("path.filename", "must not use a reserved Windows device name")
		}
	}
	return nil
}

// RelativePath 检查指定平台的相对路径形式；Windows 的驱动器相对形式也被拒绝。
// 此方法允许内部点目录和父目录；禁止向外穿越时使用 LexicallyLocalPath。
func RelativePath(value string, style PathStyle) error {
	if !pathStyleValid(style) {
		return check.NewRuleError("path.style", "unknown path style")
	}
	if err := PathText(value); err != nil {
		return err
	}
	if value[0] == '/' || style == PathWindows && (value[0] == '\\' || strings.ContainsRune(value, ':')) {
		return check.NewViolation("path.relative", "must be a relative path without a root or drive")
	}
	return pathComponentsValid(value, style)
}

// AbsolutePath 检查 POSIX 根路径、Windows 驱动器根路径或 UNC 共享路径的明确形式。
// Windows 设备命名空间不在本方法的接受范围内；根路径本身合法。
func AbsolutePath(value string, style PathStyle) error {
	if !pathStyleValid(style) {
		return check.NewRuleError("path.style", "unknown path style")
	}
	if err := PathText(value); err != nil {
		return err
	}
	if style == PathPOSIX {
		if value[0] != '/' {
			return check.NewViolation("path.absolute", "must start with a POSIX root separator")
		}
		return pathComponentsValid(value, style)
	}
	if len(value) >= 3 && pathASCIILetter(value[0]) && value[1] == ':' && pathWindowsSeparator(value[2]) {
		return pathComponentsValid(value[3:], style)
	}
	if len(value) >= 2 && pathWindowsSeparator(value[0]) && pathWindowsSeparator(value[1]) {
		parts := pathParts(value[2:], style)
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" || parts[0] == "." || parts[0] == "?" {
			return check.NewViolation("path.absolute", "must identify a UNC server and share")
		}
		if err := FileName(parts[0], style); err != nil {
			return err
		}
		if err := FileName(parts[1], style); err != nil {
			return err
		}
		return pathComponentsValid(value[2:], style)
	}
	return check.NewViolation("path.absolute", "must identify a Windows drive root or UNC share")
}

// LexicallyLocalPath 拒绝词法上逃出相对根目录的父目录，不做符号链接或真实路径解析。
// 例如 a/../b 合法，../b 非法；不能据此判断实际文件访问权限或链接目标。
func LexicallyLocalPath(value string, style PathStyle) error {
	if err := RelativePath(value, style); err != nil {
		return err
	}
	depth := 0
	for _, part := range pathParts(value, style) {
		switch part {
		case "", ".":
		case "..":
			if depth == 0 {
				return check.NewViolation("path.local", "parent traversal must not escape the relative root")
			}
			depth--
		default:
			depth++
		}
	}
	return nil
}

func pathStyleValid(style PathStyle) bool { return style == PathPOSIX || style == PathWindows }

func pathParts(value string, style PathStyle) []string {
	if style == PathWindows {
		value = strings.ReplaceAll(value, `\`, "/")
	}
	return strings.Split(value, "/")
}

func pathComponentsValid(value string, style PathStyle) error {
	for _, part := range pathParts(value, style) {
		if part == "" || part == "." || part == ".." {
			continue
		}
		if err := FileName(part, style); err != nil {
			return err
		}
	}
	return nil
}

func pathWindowsDevice(stem string) bool {
	if !strings.HasPrefix(stem, "COM") && !strings.HasPrefix(stem, "LPT") {
		return false
	}
	suffix := stem[3:]
	return len(suffix) == 1 && suffix[0] >= '1' && suffix[0] <= '9' || suffix == "¹" || suffix == "²" || suffix == "³"
}

func pathWindowsSeparator(c byte) bool { return c == '/' || c == '\\' }
func pathASCIILetter(c byte) bool      { return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }
