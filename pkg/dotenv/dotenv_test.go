package dotenv

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDoesNotOverrideExistingEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := "# comment\nRBT_A=from-file\nexport RBT_B=\"quoted\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RBT_A", "from-env")
	t.Setenv("RBT_B", "")
	if err := os.Unsetenv("RBT_B"); err != nil {
		t.Fatal(err)
	}

	if err := Load(path); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got := os.Getenv("RBT_A"); got != "from-env" {
		t.Errorf("RBT_A = %q, want from-env", got)
	}
	if got := os.Getenv("RBT_B"); got != "quoted" {
		t.Errorf("RBT_B = %q, want quoted", got)
	}
}

func TestLoadMissingFileIsNotError(t *testing.T) {
	if err := Load(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestTypedGetters(t *testing.T) {
	t.Setenv("RBT_PORT", "8082")
	t.Setenv("RBT_TIMEOUT", "750ms")
	t.Setenv("RBT_BAD", "x")

	if n, err := Int("RBT_PORT", 1); err != nil || n != 8082 {
		t.Errorf("Int() = %d, %v", n, err)
	}
	if d, err := Duration("RBT_TIMEOUT", time.Second); err != nil || d != 750*time.Millisecond {
		t.Errorf("Duration() = %v, %v", d, err)
	}
	if _, err := Int("RBT_BAD", 1); err == nil {
		t.Error("Int() must fail on non-number")
	}
	if got := String("RBT_UNSET_X", "def"); got != "def" {
		t.Errorf("String() = %q, want def", got)
	}
}
