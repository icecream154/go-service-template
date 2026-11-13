package repo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindRootFromDirDiscoversAnyGoModule(t *testing.T) {
	// 验证不依赖特定项目模块名，任意合法 go.mod 在深层子目录中均可向上定位。
	base := t.TempDir()
	moduleRoot := filepath.Join(base, "arbitrary-service")
	nested := filepath.Join(moduleRoot, "tests", "integration", "billing", "v1")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	goModContent := "// comment header\n\nmodule example.org/arbitrary-service\n\ngo 1.26.0\n"
	if err := os.WriteFile(filepath.Join(moduleRoot, "go.mod"), []byte(goModContent), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := FindRootFromDir(nested)
	if err != nil {
		t.Fatalf("FindRootFromDir returned unexpected error: %v", err)
	}
	if got != moduleRoot {
		t.Fatalf("FindRootFromDir = %q, want %q", got, moduleRoot)
	}
}

func TestFindRootExplicitAndMissingCases(t *testing.T) {
	explicitDir := t.TempDir()
	got, err := FindRoot(explicitDir)
	if err != nil || got != explicitDir {
		t.Fatalf("FindRoot(explicit) = (%q, %v), want (%q, nil)", got, err, explicitDir)
	}

	// 空目录且确保其上方临时树不存在 go.mod 时（当系统临时目录不含 go.mod），忽略目录型 go.mod。
	isolated := t.TempDir()
	if err := os.Mkdir(filepath.Join(isolated, "go.mod"), 0o700); err != nil {
		t.Fatal(err)
	}
	if hasGoModuleFile(filepath.Join(isolated, "go.mod")) {
		t.Fatal("directory named go.mod must not be treated as module file")
	}

	badMod := filepath.Join(isolated, "invalid.mod")
	if err := os.WriteFile(badMod, []byte("not_a_module_declaration\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if hasGoModuleFile(badMod) {
		t.Fatal("file without module directive must not be accepted")
	}
}
