package security

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"mcpipe/internal/config"
)

func TestRedactorRedactsNamedValuesAndTokens(t *testing.T) {
	redactor := NewRedactor()
	redactor.AddNamedValue("BRAVE_API_KEY", "super-secret-value")
	got := redactor.RedactString(`Authorization: Bearer abcdefghijklmnop key=super-secret-value`)
	if strings.Contains(got, "super-secret-value") || strings.Contains(got, "abcdefghijklmnop") {
		t.Fatalf("secret leaked after redaction: %s", got)
	}
}

func TestSandboxPathRejectsEscape(t *testing.T) {
	policy := DefaultPolicy()
	policy.OutputDir = t.TempDir()
	if _, err := policy.SandboxPath("filesystem.write_file", "../escape.md"); err == nil {
		t.Fatal("expected escape error")
	}
	got, err := policy.SandboxPath("filesystem.write_file", "safe.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, policy.OutputDir) {
		t.Fatalf("expected path inside output dir, got %s", got)
	}
}

func TestSandboxPathRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink creation often requires elevated privileges")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy()
	policy.OutputDir = root
	if _, err := policy.SandboxPath("filesystem.write_file", "link/escape.md"); err == nil {
		t.Fatal("expected symlink escape error")
	}
}

func TestLockBuildAndVerify(t *testing.T) {
	dir := t.TempDir()
	pipeline := filepath.Join(dir, "pipeline.json")
	if err := os.WriteFile(pipeline, []byte(`{"version":"1.0.0","steps":[{"id":"a","prompt":{"user":"x"},"outputs":{"out":"{{ response.text }}"}}],"defaults":{"llm":{"backend":"ollama","model":"qwen"}}}`), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := config.LoadFile(pipeline)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := BuildLock(pipeline, p, time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(dir, "mcpipe.lock")
	if err := WriteLock(lockPath, lock); err != nil {
		t.Fatal(err)
	}
	if err := VerifyLock(lockPath, pipeline); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pipeline, []byte(`{"changed":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyLock(lockPath, pipeline); err == nil {
		t.Fatal("expected lock mismatch")
	}
}

func TestSignAndVerify(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "pipeline.json")
	privateKey := filepath.Join(dir, "mcpipe.key")
	publicKey := filepath.Join(dir, "mcpipe.pub")
	sig := filepath.Join(dir, "pipeline.sig")
	if err := os.WriteFile(file, []byte(`{"version":"1.0.0"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := GenerateKeypair(privateKey, publicKey); err != nil {
		t.Fatal(err)
	}
	if err := SignFile(file, privateKey, sig); err != nil {
		t.Fatal(err)
	}
	if err := VerifySignature(file, publicKey, sig); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"version":"2.0.0"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := VerifySignature(file, publicKey, sig); err == nil {
		t.Fatal("expected signature verification failure")
	}
}
