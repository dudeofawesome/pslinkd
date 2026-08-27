package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultPollInterval      = 200 * time.Millisecond
	DefaultDisconnectFailure = 3
	DefaultLogLevel          = "info"
)

type Config struct {
	Audio    Audio    `yaml:"audio"`
	Controls Controls `yaml:"controls"`
	Polling  Polling  `yaml:"polling"`
	Logging  Logging  `yaml:"logging"`
}

type Controls struct {
	Enabled bool `yaml:"enabled"`
}

type Audio struct {
	HeadsetSink    string `yaml:"headset_sink,omitempty"`
	FallbackSink   string `yaml:"fallback_sink"`
	HeadsetSource  string `yaml:"headset_source,omitempty"`
	FallbackSource string `yaml:"fallback_source,omitempty"`
}

type Polling struct {
	Interval           Duration `yaml:"interval"`
	DisconnectFailures int      `yaml:"disconnect_failures"`
}

type Logging struct {
	Level string `yaml:"level"`
}

type Duration struct {
	time.Duration
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Tag != "!!str" {
		return fmt.Errorf("duration must be a string")
	}
	value, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", node.Value, err)
	}
	d.Duration = value
	return nil
}

func (d Duration) MarshalYAML() (any, error) {
	return d.String(), nil
}

func Defaults() Config {
	return Config{
		Polling: Polling{
			Interval:           Duration{DefaultPollInterval},
			DisconnectFailures: DefaultDisconnectFailure,
		},
		Logging: Logging{Level: DefaultLogLevel},
	}
}

func Load(path string) (Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()

	cfg, err := Decode(file)
	if err != nil {
		return Config{}, fmt.Errorf("load config %q: %w", path, err)
	}
	return cfg, nil
}

func Decode(reader io.Reader) (Config, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return Config{}, fmt.Errorf("read YAML: %w", err)
	}
	if err := validateStringScalars(data); err != nil {
		return Config{}, err
	}

	cfg := Defaults()
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode YAML: %w", err)
	}

	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Config{}, errors.New("configuration must contain one YAML document")
		}
		return Config{}, fmt.Errorf("decode trailing YAML: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func validateStringScalars(data []byte) error {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("decode YAML: %w", err)
	}
	if len(document.Content) == 0 {
		return nil
	}

	stringPaths := map[string]bool{
		"audio.headset_sink":    true,
		"audio.fallback_sink":   true,
		"audio.headset_source":  true,
		"audio.fallback_source": true,
		"polling.interval":      true,
		"logging.level":         true,
	}
	optionalNonempty := map[string]bool{
		"audio.headset_sink":    true,
		"audio.headset_source":  true,
		"audio.fallback_source": true,
	}
	var walk func(*yaml.Node, string) error
	walk = func(node *yaml.Node, path string) error {
		if node.Kind != yaml.MappingNode {
			return nil
		}
		for index := 0; index < len(node.Content); index += 2 {
			key := node.Content[index]
			value := node.Content[index+1]
			childPath := key.Value
			if path != "" {
				childPath = path + "." + key.Value
			}
			if stringPaths[childPath] && value.Tag != "!!str" {
				return fmt.Errorf("%s must be a string", childPath)
			}
			if optionalNonempty[childPath] && strings.TrimSpace(value.Value) == "" {
				return fmt.Errorf("%s must be nonempty when set", childPath)
			}
			if err := walk(value, childPath); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(document.Content[0], "")
}

func (cfg Config) Validate() error {
	if strings.TrimSpace(cfg.Audio.FallbackSink) == "" {
		return errors.New("audio.fallback_sink must be nonempty")
	}

	hasHeadsetSource := strings.TrimSpace(cfg.Audio.HeadsetSource) != ""
	hasFallbackSource := strings.TrimSpace(cfg.Audio.FallbackSource) != ""
	if hasHeadsetSource && !hasFallbackSource {
		return errors.New("audio.headset_source requires audio.fallback_source")
	}

	if cfg.Polling.Interval.Duration < 50*time.Millisecond ||
		cfg.Polling.Interval.Duration > 10*time.Second {
		return errors.New("polling.interval must be between 50ms and 10s")
	}
	if cfg.Polling.DisconnectFailures < 1 || cfg.Polling.DisconnectFailures > 50 {
		return errors.New("polling.disconnect_failures must be between 1 and 50")
	}

	switch cfg.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return errors.New("logging.level must be debug, info, warn, or error")
	}
	return nil
}

func DefaultPath(getenv func(string) string, userConfigDir func() (string, error)) (string, error) {
	if xdg := getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "pslinkd", "config.yaml"), nil
	}
	base, err := userConfigDir()
	if err != nil {
		return "", fmt.Errorf("resolve user configuration directory: %w", err)
	}
	return filepath.Join(base, "pslinkd", "config.yaml"), nil
}
