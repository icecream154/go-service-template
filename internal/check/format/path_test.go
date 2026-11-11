package format_test

import (
	"errors"
	"testing"

	"go-service-template/internal/check"
	"go-service-template/internal/check/format"
)

func TestPathText(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr error
	}{
		// 用含中文和空格的路径验证合法 UTF-8 文本被保留。
		{name: "Unicode path", input: "文档/我的记录.txt"},
		// 用仅含空格的输入验证该入口只要求文本非空。
		{name: "space only", input: " "},
		// 用空字符串验证文本的非空要求。
		{name: "empty input", input: "", wantErr: check.ErrInvalidValue},
		// 用单个非法字节验证路径的 UTF-8 要求。
		{name: "invalid UTF-8", input: "name\xff", wantErr: check.ErrInvalidValue},
		// 把 NUL 放在正常路径中间，验证任何位置的 NUL 都会被拒绝。
		{name: "embedded NUL", input: "dir\x00/file", wantErr: check.ErrInvalidValue},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertPathError(t, format.PathText(test.input), test.wantErr)
		})
	}
}

type pathCase struct {
	name    string
	input   string
	style   format.PathStyle
	wantErr error
}

func TestFileName(t *testing.T) {
	testPathCheck(t, format.FileName, []pathCase{
		// 用含空格的 Unicode 文件名验证有效的单个 POSIX 片段。
		{name: "POSIX filename", input: "我的 文档.txt", style: format.PathPOSIX},
		// POSIX 允许反斜杠作为文件名字符，也保留尾部点。
		{name: "POSIX backslash and dot", input: "report\\draft.", style: format.PathPOSIX},
		// Windows 文件名可包含扩展名，且允许内部空格。
		{name: "Windows filename", input: "Report Draft.txt", style: format.PathWindows},
		// 同时给出非法样式和空输入，验证规则错误优先于输入检查。
		{name: "unknown style", input: "", style: format.PathStyle(255), wantErr: check.ErrInvalidRule},
		// 用带 NUL 的文本验证入口返回底层文本错误。
		{name: "invalid path text", input: "a\x00b", style: format.PathPOSIX, wantErr: check.ErrInvalidValue},
		// 用当前目录标记验证它不能作为单个文件名。
		{name: "current directory", input: ".", style: format.PathPOSIX, wantErr: check.ErrInvalidValue},
		// 用父目录标记验证它同样不能作为单个文件名。
		{name: "parent directory", input: "..", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用斜杠分隔两个片段，验证单个 POSIX 文件名不接受路径。
		{name: "POSIX separator", input: "dir/file", style: format.PathPOSIX, wantErr: check.ErrInvalidValue},
		// 用反斜杠分隔两个片段，验证 Windows 将其视为路径分隔符。
		{name: "Windows separator", input: "dir\\file", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用冒号验证 Windows 保留字符的拒绝逻辑。
		{name: "Windows reserved character", input: "a:b.txt", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用普通名称附加空格，验证 Windows 拒绝尾部空格。
		{name: "Windows trailing space", input: "report ", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用普通名称附加点，验证 Windows 拒绝尾部点。
		{name: "Windows trailing dot", input: "report.", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用非 NUL 的控制字符验证 Windows 自有的字符限制。
		{name: "Windows control character", input: "re\tport", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用小写设备名、内部空格和扩展名验证设备名按规范化主干识别。
		{name: "console device stem", input: "con .txt", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用打印机设备名验证它属于固定保留集合。
		{name: "printer device", input: "PRN", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用辅助设备名验证扩展名不改变保留状态。
		{name: "auxiliary device", input: "AUX.log", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用空设备名验证设备集合包含 NUL。
		{name: "null device", input: "NUL", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用控制台输入设备名验证带美元符号的保留名称。
		{name: "console input device", input: "CONIN$", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用控制台输出设备名验证相应的保留名称。
		{name: "console output device", input: "CONOUT$", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用编号下界验证串口设备识别。
		{name: "serial device", input: "COM1", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用编号上界验证并口设备识别。
		{name: "parallel device", input: "LPT9", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用第一种上标数字验证设备编号支持特殊字符。
		{name: "superscript one device", input: "COM¹", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用第二种上标数字验证并口设备的同类限制。
		{name: "superscript two device", input: "LPT²", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用第三种上标数字验证最后一个特殊编号。
		{name: "superscript three device", input: "COM³", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用 0 验证串口保留编号从 1 开始。
		{name: "zero serial number", input: "COM0", style: format.PathWindows},
		// 用两位数字验证设备规则只识别指定的单个编号。
		{name: "two digit serial number", input: "COM10", style: format.PathWindows},
	})
}

func TestRelativePath(t *testing.T) {
	testPathCheck(t, format.RelativePath, []pathCase{
		// 用点目录与父目录验证相对语法允许这两种内部片段。
		{name: "POSIX relative with dot segments", input: "dir/./../file.txt", style: format.PathPOSIX},
		// 用开头的父目录验证此入口只判断路径形式。
		{name: "leading parent segment", input: "../file.txt", style: format.PathPOSIX},
		// 用两种分隔符组合验证 Windows 片段拆分。
		{name: "Windows mixed separators", input: "dir\\child/file.txt", style: format.PathWindows},
		// 用有效文本和未知样式验证规则错误分类。
		{name: "unknown style", input: "file.txt", style: format.PathStyle(255), wantErr: check.ErrInvalidRule},
		// 用空输入验证文本要求仍然适用。
		{name: "empty input", input: "", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用开头斜杠验证 POSIX 根路径被排除。
		{name: "POSIX root", input: "/file.txt", style: format.PathPOSIX, wantErr: check.ErrInvalidValue},
		// 用开头反斜杠验证 Windows 根路径被排除。
		{name: "Windows root", input: "\\file.txt", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用缺少根分隔符的驱动器写法验证 Windows 拒绝驱动器相对路径。
		{name: "Windows drive relative", input: "C:file.txt", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用内部设备片段验证相对路径检查验证所有普通片段。
		{name: "reserved component", input: "dir\\NUL\\file.txt", style: format.PathWindows, wantErr: check.ErrInvalidValue},
	})
}

func TestAbsolutePath(t *testing.T) {
	testPathCheck(t, format.AbsolutePath, []pathCase{
		// 用单独的斜杠验证 POSIX 根本身是绝对路径。
		{name: "POSIX root", input: "/", style: format.PathPOSIX},
		// 用多级 POSIX 路径和点目录验证根后的合法片段。
		{name: "POSIX child path", input: "/dir/./../file.txt", style: format.PathPOSIX},
		// 用大写驱动器和反斜杠验证 Windows 根形式。
		{name: "Windows drive root", input: "C:\\", style: format.PathWindows},
		// 用小写驱动器和斜杠验证另一种合法 Windows 根形式。
		{name: "Windows drive with forward slashes", input: "c:/dir/file.txt", style: format.PathWindows},
		// 用完整服务器和共享名称验证 UNC 根形式。
		{name: "UNC share root", input: "\\\\server\\share", style: format.PathWindows},
		// 在 UNC 根后加子路径，验证其普通片段可继续通过校验。
		{name: "UNC child path", input: "//server/share\\dir/file.txt", style: format.PathWindows},
		// 用未知样式验证规则错误分类。
		{name: "unknown style", input: "/file.txt", style: format.PathStyle(255), wantErr: check.ErrInvalidRule},
		// 用 NUL 验证有根的文本也必须通过路径文本检查。
		{name: "invalid path text", input: "/a\x00b", style: format.PathPOSIX, wantErr: check.ErrInvalidValue},
		// 去掉 POSIX 根分隔符，验证相对路径被拒绝。
		{name: "POSIX relative path", input: "dir/file.txt", style: format.PathPOSIX, wantErr: check.ErrInvalidValue},
		// 用没有根分隔符的驱动器写法验证 Windows 拒绝驱动器相对路径。
		{name: "Windows drive relative", input: "C:file.txt", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用数字替代驱动器字母验证驱动器的词法要求。
		{name: "Windows numeric drive", input: "1:/file.txt", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用有驱动器的绝对路径验证其后仍会拒绝设备片段。
		{name: "Windows drive with reserved component", input: "C:\\NUL\\file.txt", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用只有服务器名称的 UNC 形式验证必须提供共享名称。
		{name: "UNC missing share", input: "\\\\server", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用重复分隔符构造空服务器名，验证两个名称都须非空。
		{name: "UNC empty server", input: "///share", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用空共享名和后续片段验证共享名不可省略。
		{name: "UNC empty share", input: "//server//file.txt", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用点号服务器名验证设备命名空间不会被识别为普通共享。
		{name: "Windows device namespace", input: "//./disk/file.txt", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用问号服务器名验证扩展设备命名空间同样被拒绝。
		{name: "Windows extended namespace", input: "//?/C:/file.txt", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用保留设备名作为服务器验证服务器名称也经过文件名检查。
		{name: "UNC reserved server", input: "//NUL/share", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用保留设备名作为共享验证共享名称也经过文件名检查。
		{name: "UNC reserved share", input: "//server/AUX", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 让服务器和共享有效，验证其后的设备片段仍会被拒绝。
		{name: "UNC reserved child", input: "//server/share/NUL", style: format.PathWindows, wantErr: check.ErrInvalidValue},
	})
}

func TestLexicallyLocalPath(t *testing.T) {
	testPathCheck(t, format.LexicallyLocalPath, []pathCase{
		// 用当前目录验证词法上的相对根本身有效。
		{name: "relative root", input: ".", style: format.PathPOSIX},
		// 用一次进入和一次返回验证允许恰好回到相对根。
		{name: "balanced parent segment", input: "dir/../file.txt", style: format.PathPOSIX},
		// 用连续分隔符和点目录验证它们不会增加目录深度。
		{name: "ignored empty and current segments", input: "dir//./../file.txt", style: format.PathPOSIX},
		// 用 Windows 混合分隔符验证父目录的计数规则相同。
		{name: "Windows balanced path", input: "dir\\../file.txt", style: format.PathWindows},
		// 用开头的父目录验证零深度时直接拒绝。
		{name: "leading escape", input: "../file.txt", style: format.PathPOSIX, wantErr: check.ErrInvalidValue},
		// 用多一个父目录验证回到相对根后仍不能继续向上。
		{name: "escape after descent", input: "dir/../../file.txt", style: format.PathPOSIX, wantErr: check.ErrInvalidValue},
		// 用 Windows 绝对路径验证入口首先要求相对形式。
		{name: "absolute path", input: "C:\\file.txt", style: format.PathWindows, wantErr: check.ErrInvalidValue},
		// 用未知样式验证相对路径检查的规则错误原样传回。
		{name: "unknown style", input: "file.txt", style: format.PathStyle(255), wantErr: check.ErrInvalidRule},
	})
}

func testPathCheck(t *testing.T, validate func(string, format.PathStyle) error, tests []pathCase) {
	t.Helper()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertPathError(t, validate(test.input, test.style), test.wantErr)
		})
	}
}

func assertPathError(t *testing.T, err, wantErr error) {
	t.Helper()
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}
