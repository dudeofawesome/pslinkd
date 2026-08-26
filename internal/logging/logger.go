package logging

import (
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"
)

type Level int

const (
	Debug Level = iota
	Info
	Warn
	Error
)

type Fields map[string]any

type Logger struct {
	mu      sync.Mutex
	encoder *json.Encoder
	minimum Level
	now     func() time.Time
	last    map[string]time.Time
}

func New(writer io.Writer, level string, now func() time.Time) *Logger {
	if now == nil {
		now = time.Now
	}
	return &Logger{
		encoder: json.NewEncoder(writer),
		minimum: parseLevel(level),
		now:     now,
		last:    make(map[string]time.Time),
	}
}

func (logger *Logger) Event(level Level, event, message string, fields Fields) {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	logger.write(level, event, message, fields)
}

func (logger *Logger) RateLimited(
	key string,
	interval time.Duration,
	level Level,
	event string,
	message string,
	fields Fields,
) bool {
	logger.mu.Lock()
	defer logger.mu.Unlock()
	if level < logger.minimum {
		return false
	}
	now := logger.now()
	if last, found := logger.last[key]; found && now.Sub(last) < interval {
		return false
	}
	logger.last[key] = now
	logger.writeAt(now, level, event, message, fields)
	return true
}

func (logger *Logger) write(level Level, event, message string, fields Fields) {
	if level < logger.minimum {
		return
	}
	logger.writeAt(logger.now(), level, event, message, fields)
}

func (logger *Logger) writeAt(
	now time.Time,
	level Level,
	event string,
	message string,
	fields Fields,
) {
	record := make(map[string]any, len(fields)+4)
	for key, value := range fields {
		record[key] = value
	}
	record["time"] = now.UTC().Format(time.RFC3339Nano)
	record["level"] = level.String()
	record["event"] = event
	record["message"] = message
	_ = logger.encoder.Encode(record)
}

func parseLevel(level string) Level {
	switch strings.ToLower(level) {
	case "debug":
		return Debug
	case "warn":
		return Warn
	case "error":
		return Error
	default:
		return Info
	}
}

func (level Level) String() string {
	switch level {
	case Debug:
		return "debug"
	case Warn:
		return "warn"
	case Error:
		return "error"
	default:
		return "info"
	}
}
