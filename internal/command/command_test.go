package command

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dudeofawesome/pslinkd/internal/config"
)

func TestExecuteCLIValidation(t *testing.T) {
	tests := [][]string{
		nil,
		{"unknown"},
		{"run", "positional"},
		{"run", "--unknown"},
	}
	for _, args := range tests {
		if err := Execute(args, func(string) string { return "" }, func(config.Config) error {
			return nil
		}); err == nil {
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
	called := false
	err := Execute([]string{"run", "--config", path}, func(string) string { return "" }, func(cfg config.Config) error {
		called = true
		if cfg.Audio.HeadsetSink != "h" || cfg.Audio.FallbackSink != "f" {
			t.Fatalf("config = %#v", cfg)
		}
		return nil
	})
	if err != nil || !called {
		t.Fatalf("validated config did not reach runtime: called=%v, err=%v", called, err)
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
	called := false
	err := Execute([]string{"run"}, func(key string) string {
		if key == "XDG_CONFIG_HOME" {
			return dir
		}
		return ""
	}, func(config.Config) error {
		called = true
		return nil
	})
	if err != nil || !called {
		t.Fatalf("XDG config did not reach runtime: called=%v, err=%v", called, err)
	}
}

func TestExecuteRejectsInvalidConfigBeforeRuntime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("audio:\n  headset_sink: h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	err := Execute([]string{"run", "--config", path}, func(string) string { return "" }, func(config.Config) error {
		called = true
		return nil
	})
	if err == nil || called {
		t.Fatalf("invalid config: called=%v, err=%v", called, err)
	}
}
