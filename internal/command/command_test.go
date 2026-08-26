package command

import (
  "os"
  "path/filepath"
  "strings"
  "testing"
)

func TestExecuteCLIValidation(t *testing.T) {
  tests := [][]string{
    nil,
    {"unknown"},
    {"run", "positional"},
    {"run", "--unknown"},
  }
  for _, args := range tests {
    if err := Execute(args, func(string) string { return "" }); err == nil {
      t.Fatalf("Execute(%q) unexpectedly succeeded", args)
    }
  }
}

func TestExecuteConfigOverrideIsValidated(t *testing.T) {
  dir := t.TempDir()
  path := filepath.Join(dir, "config.yaml")
  if err := os.WriteFile(path, []byte("audio:\n  headset_sink: h\n  fallback_sink: f\n"), 0o600); err != nil {
    t.Fatal(err)
  }
  err := Execute([]string{"run", "--config", path}, func(string) string { return "" })
  if err == nil || !strings.Contains(err.Error(), "not implemented") {
    t.Fatalf("expected validated config to reach runtime, got %v", err)
  }
}

func TestExecuteUsesXDGDefault(t *testing.T) {
  dir := t.TempDir()
  configDir := filepath.Join(dir, "pslinkd")
  if err := os.Mkdir(configDir, 0o700); err != nil {
    t.Fatal(err)
  }
  if err := os.WriteFile(
    filepath.Join(configDir, "config.yaml"),
    []byte("audio:\n  headset_sink: h\n  fallback_sink: f\n"),
    0o600,
  ); err != nil {
    t.Fatal(err)
  }
  err := Execute([]string{"run"}, func(key string) string {
    if key == "XDG_CONFIG_HOME" {
      return dir
    }
    return ""
  })
  if err == nil || !strings.Contains(err.Error(), "not implemented") {
    t.Fatalf("expected XDG config to reach runtime, got %v", err)
  }
}
