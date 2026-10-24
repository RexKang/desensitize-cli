package files

import (
	"os"
	"path/filepath"
	"testing"
)

// 明确点名的无扩展名文件（hosts、sshd_config 这类）应当按文本处理。
func TestDiscoverExplicitExtensionless(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hosts")
	if err := os.WriteFile(path, []byte("10.1.2.3 host\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, problems, _ := Discover([]string{path}, true)
	if len(got) != 1 || len(problems) != 0 {
		t.Fatalf("应当收录该文件, got=%v problems=%v", got, problems)
	}
}
