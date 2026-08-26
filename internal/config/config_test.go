package config

import (
  "path/filepath"
  "strings"
  "testing"
  "time"
)

const minimal = `
audio:
  headset_sink: headset
  fallback_sink: fallback
`

func TestDecodeMinimalAppliesDefaults(t *testing.T) {
  cfg, err := Decode(strings.NewReader(minimal))
  if err != nil {
    t.Fatal(err)
  }
  if cfg.Polling.Interval.Duration != 200*time.Millisecond {
    t.Errorf("interval = %s", cfg.Polling.Interval.Duration)
  }
  if cfg.Polling.DisconnectFailures != 3 {
    t.Errorf("disconnect failures = %d", cfg.Polling.DisconnectFailures)
  }
  if cfg.Logging.Level != "info" {
    t.Errorf("log level = %q", cfg.Logging.Level)
  }
}

func TestDecodeFullConfigWithCommentsAndSources(t *testing.T) {
  cfg, err := Decode(strings.NewReader(`
# strict YAML still permits comments
audio:
  headset_sink: "on"
  fallback_sink: "off"
  headset_source: headset-input
  fallback_source: fallback-input
polling:
  interval: 50ms
  disconnect_failures: 50
logging:
  level: debug
`))
  if err != nil {
    t.Fatal(err)
  }
  if cfg.Audio.HeadsetSink != "on" || cfg.Audio.FallbackSource != "fallback-input" {
    t.Fatalf("unexpected audio config: %#v", cfg.Audio)
  }
}

func TestDecodeRejectsInvalidConfigurations(t *testing.T) {
  tests := map[string]string{
    "unknown key":        minimal + "unknown: true\n",
    "duplicate key":      "audio:\n  headset_sink: a\n  headset_sink: b\n  fallback_sink: c\n",
    "missing sink":       "audio:\n  headset_sink: a\n",
    "empty sink":         "audio:\n  headset_sink: ''\n  fallback_sink: b\n",
    "one source":         minimal + "  headset_source: input\n",
    "duration type":      minimal + "polling:\n  interval: 200\n",
    "invalid duration":   minimal + "polling:\n  interval: fast\n",
    "short interval":     minimal + "polling:\n  interval: 49ms\n",
    "long interval":      minimal + "polling:\n  interval: 11s\n",
    "failures type":      minimal + "polling:\n  disconnect_failures: three\n",
    "few failures":       minimal + "polling:\n  disconnect_failures: 0\n",
    "many failures":      minimal + "polling:\n  disconnect_failures: 51\n",
    "invalid level":      minimal + "logging:\n  level: verbose\n",
    "implicit node name": "audio:\n  headset_sink: on\n  fallback_sink: false\n",
    "multiple documents": minimal + "---\naudio: {}\n",
  }
  for name, input := range tests {
    t.Run(name, func(t *testing.T) {
      if _, err := Decode(strings.NewReader(input)); err == nil {
        t.Fatal("expected an error")
      }
    })
  }
}

func TestDefaultPath(t *testing.T) {
  t.Run("XDG", func(t *testing.T) {
    path, err := DefaultPath(func(key string) string {
      if key == "XDG_CONFIG_HOME" {
        return "/xdg"
      }
      return ""
    }, func() (string, error) { return "/user", nil })
    if err != nil {
      t.Fatal(err)
    }
    if path != filepath.Join("/xdg", "pslinkd", "config.yaml") {
      t.Fatalf("path = %q", path)
    }
  })

  t.Run("user config directory", func(t *testing.T) {
    path, err := DefaultPath(func(string) string { return "" }, func() (string, error) {
      return "/user", nil
    })
    if err != nil {
      t.Fatal(err)
    }
    if path != filepath.Join("/user", "pslinkd", "config.yaml") {
      t.Fatalf("path = %q", path)
    }
  })
}
