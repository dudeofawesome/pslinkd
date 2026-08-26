package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLoggerWritesOneJSONObjectPerLine(t *testing.T) {
	var output bytes.Buffer
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.FixedZone("test", -7*60*60))
	logger := New(&output, "debug", func() time.Time { return now })
	logger.Event(Info, "headset_connected", "headset radio link connected", Fields{
		"adapter_present":   true,
		"headset_connected": true,
	})
	logger.Event(Warn, "hid_failure", "feature report failed", Fields{"error": "EPIPE"})

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %d, output = %q", len(lines), output.String())
	}
	for _, line := range lines {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid JSON line %q: %v", line, err)
		}
		for _, key := range []string{"time", "level", "event", "message"} {
			if _, found := record[key]; !found {
				t.Fatalf("record lacks %q: %#v", key, record)
			}
		}
	}
}

func TestLoggerFiltersBelowConfiguredLevel(t *testing.T) {
	var output bytes.Buffer
	logger := New(&output, "warn", time.Now)
	logger.Event(Info, "ignored", "ignored", nil)
	logger.Event(Warn, "kept", "kept", nil)
	if strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), `"event":"kept"`) {
		t.Fatalf("output = %q", output.String())
	}
}

func TestRateLimitedLogsFirstAndAfterInterval(t *testing.T) {
	var output bytes.Buffer
	now := time.Unix(100, 0)
	logger := New(&output, "debug", func() time.Time { return now })
	for range 3 {
		logger.RateLimited("hid:EPIPE", time.Minute, Warn, "hid_failure", "failed", nil)
	}
	if got := strings.Count(output.String(), "\n"); got != 1 {
		t.Fatalf("initial lines = %d", got)
	}
	now = now.Add(time.Minute)
	logger.RateLimited("hid:EPIPE", time.Minute, Warn, "hid_failure", "failed", nil)
	if got := strings.Count(output.String(), "\n"); got != 2 {
		t.Fatalf("lines after interval = %d", got)
	}
}
