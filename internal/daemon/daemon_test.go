package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dudeofawesome/pslinkd/internal/audio"
	"github.com/dudeofawesome/pslinkd/internal/config"
	"github.com/dudeofawesome/pslinkd/internal/discovery"
	"github.com/dudeofawesome/pslinkd/internal/logging"
	"github.com/dudeofawesome/pslinkd/internal/polling"
	"github.com/dudeofawesome/pslinkd/internal/state"
)

func TestObserverWritesRequiredEventsAndRateLimitsFailures(t *testing.T) {
	var output bytes.Buffer
	logger := logging.New(&output, "debug", func() time.Time { return time.Unix(100, 0) })
	observer := &observer{
		logger:        logger,
		selections:    make(chan discovery.Candidate, 1),
		desiredStates: make(chan audio.Desired, 1),
		targets: audio.Targets{
			HeadsetSink:    "headset",
			FallbackSink:   "speakers",
			HeadsetSource:  "headset-mic",
			FallbackSource: "desk-mic",
		},
	}
	candidate := discovery.Candidate{Syspath: "/sys/a", Devnode: "/dev/hidraw3"}
	observer.SelectionChanged(discovery.Candidate{})
	observer.SelectionChanged(candidate)
	observer.MultipleCandidates([]discovery.Candidate{
		candidate,
		{Syspath: "/sys/b", Devnode: "/dev/hidraw8"},
	})
	observer.ConnectionChanged(state.Connection{AdapterPresent: true, HeadsetConnected: true})
	observer.ConnectionChanged(state.Connection{AdapterPresent: true})
	for range 3 {
		observer.HIDFailure(candidate.Devnode, errors.New("EPIPE"))
	}
	observer.HIDRecovered(candidate.Devnode)
	routingError := &audio.TargetError{
		Kind:       audio.Source,
		TargetName: "headset-mic",
		Err:        errors.New("WirePlumber unavailable"),
	}
	for attempt := 1; attempt <= 3; attempt++ {
		observer.AudioActionRetrying(7, audio.Desired{HeadsetConnected: true}, attempt, routingError)
	}
	observer.AudioActionSucceeded(7, audio.Desired{HeadsetConnected: true}, 4)
	observer.SelectionChanged(discovery.Candidate{})

	records := decodeRecords(t, output.String())
	wantEvents := []string{
		"adapter_added",
		"adapter_removed",
		"multiple_adapters",
		"headset_connected",
		"headset_disconnected",
		"hid_failure",
		"hid_recovered",
		"audio_action_retrying",
		"audio_action_succeeded",
	}
	for _, event := range wantEvents {
		if countEvents(records, event) == 0 {
			t.Errorf("missing event %q in %#v", event, records)
		}
	}
	if got := countEvents(records, "hid_failure"); got != 1 {
		t.Errorf("HID failure events = %d, want 1", got)
	}
	if got := countEvents(records, "audio_action_retrying"); got != 1 {
		t.Errorf("audio retry events = %d, want 1", got)
	}
	for _, record := range records {
		for _, key := range []string{"time", "level", "event", "message"} {
			if _, found := record[key]; !found {
				t.Errorf("event lacks %q: %#v", key, record)
			}
		}
	}
}

func TestObserverCarriesSelectedUSBIdentityIntoConnectedAudioAction(t *testing.T) {
	observer := &observer{
		logger:        logging.New(&bytes.Buffer{}, "debug", nil),
		selections:    make(chan discovery.Candidate, 1),
		desiredStates: make(chan audio.Desired, 1),
	}
	candidate := discovery.Candidate{
		Syspath:          "/sys/hidraw/a",
		Devnode:          "/dev/hidraw3",
		USBParentSyspath: "/sys/usb/1-2",
		USBSerial:        "adapter-a",
	}
	observer.SelectionChanged(candidate)
	observer.ConnectionChanged(state.Connection{AdapterPresent: true, HeadsetConnected: true})
	desired := <-observer.desiredStates
	want := audio.USBIdentity{
		Syspath:    candidate.USBParentSyspath,
		Serial:     candidate.USBSerial,
		HIDSyspath: candidate.Syspath,
		HIDDevnode: candidate.Devnode,
	}
	if desired.USB != want {
		t.Fatalf("desired USB identity = %#v, want %#v", desired.USB, want)
	}
}

func TestObserverLogsAutomaticTargetAmbiguity(t *testing.T) {
	var output bytes.Buffer
	observer := &observer{logger: logging.New(&output, "debug", nil)}
	priority := 100
	observer.AutomaticTargetSelected(
		audio.Sink,
		audio.USBIdentity{
			Syspath: "/sys/usb/1-2", Serial: "serial-a",
			HIDSyspath: "/sys/hid/a", HIDDevnode: "/dev/hidraw3",
		},
		"20",
		"alpha",
		[]audio.ResolvedCandidate{
			{Name: "alpha", Priority: &priority},
			{Name: "zeta"},
		},
	)
	records := decodeRecords(t, output.String())
	if len(records) != 1 || records[0]["level"] != "warn" ||
		records[0]["target_name"] != "alpha" || records[0]["audio_device_id"] != "20" {
		t.Fatalf("automatic target record = %#v", records)
	}
	if names, ok := records[0]["candidate_names"].([]any); !ok || len(names) != 2 {
		t.Fatalf("candidate names = %#v", records[0]["candidate_names"])
	}
}

type lifecycleBackend struct {
	monitorStarted chan struct{}
	monitorStopped chan struct{}
	enumerateErr   error
}

func (backend *lifecycleBackend) Enumerate() ([]discovery.Candidate, error) {
	return nil, backend.enumerateErr
}

func (backend *lifecycleBackend) Monitor(ctx context.Context, _ func(discovery.Event) error) error {
	close(backend.monitorStarted)
	<-ctx.Done()
	close(backend.monitorStopped)
	return nil
}

type lifecycleTicker struct {
	ticks   chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func (ticker *lifecycleTicker) C() <-chan time.Time { return ticker.ticks }
func (ticker *lifecycleTicker) Stop() {
	ticker.once.Do(func() { close(ticker.stopped) })
}

type lifecyclePollClock struct {
	ticker *lifecycleTicker
}

func (clock lifecyclePollClock) NewTicker(time.Duration) polling.Ticker { return clock.ticker }

type blockingSetter struct {
	started  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func (setter *blockingSetter) SetDefault(ctx context.Context, _ audio.Kind, _ audio.Target) error {
	setter.once.Do(func() { close(setter.started) })
	<-ctx.Done()
	close(setter.canceled)
	return ctx.Err()
}

type unusedRetryClock struct{}

func (unusedRetryClock) NewTimer(time.Duration) audio.Timer {
	panic("retry timer unexpectedly created")
}

func TestRunCancellationStopsAllWorkers(t *testing.T) {
	backend := &lifecycleBackend{
		monitorStarted: make(chan struct{}),
		monitorStopped: make(chan struct{}),
	}
	ticker := &lifecycleTicker{
		ticks:   make(chan time.Time),
		stopped: make(chan struct{}),
	}
	setter := &blockingSetter{started: make(chan struct{}), canceled: make(chan struct{})}
	var output bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, testConfig(), logging.New(&output, "debug", nil), Dependencies{
			DiscoveryBackend: backend,
			OpenReader: func(string) (polling.Reader, error) {
				return nil, errors.New("reader unexpectedly opened without an adapter")
			},
			PollClock:   lifecyclePollClock{ticker: ticker},
			AudioSetter: setter,
			RetryClock:  unusedRetryClock{},
		})
	}()

	await(t, backend.monitorStarted, "discovery monitor did not start")
	await(t, setter.started, "fallback audio action did not start")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not stop promptly")
	}
	await(t, backend.monitorStopped, "discovery monitor did not stop")
	await(t, setter.canceled, "audio subprocess context was not canceled")
	await(t, ticker.stopped, "poll ticker was not stopped")

	records := decodeRecords(t, output.String())
	if countEvents(records, "daemon_start") != 1 || countEvents(records, "daemon_stop") != 1 {
		t.Fatalf("daemon lifecycle records = %#v", records)
	}
}

func TestDiscoveryFailureIsLoggedAndReturned(t *testing.T) {
	backend := &lifecycleBackend{
		monitorStarted: make(chan struct{}),
		monitorStopped: make(chan struct{}),
		enumerateErr:   errors.New("udev unavailable"),
	}
	ticker := &lifecycleTicker{ticks: make(chan time.Time), stopped: make(chan struct{})}
	var output bytes.Buffer
	err := Run(context.Background(), testConfig(), logging.New(&output, "debug", nil), Dependencies{
		DiscoveryBackend: backend,
		OpenReader:       func(string) (polling.Reader, error) { return nil, errors.New("unused") },
		PollClock:        lifecyclePollClock{ticker: ticker},
		AudioSetter:      &immediateSetter{},
		RetryClock:       unusedRetryClock{},
	})
	if err == nil || !strings.Contains(err.Error(), "udev unavailable") {
		t.Fatalf("error = %v", err)
	}
	records := decodeRecords(t, output.String())
	if countEvents(records, "discovery_fatal") != 1 {
		t.Fatalf("records = %#v", records)
	}
}

type immediateSetter struct{}

func (*immediateSetter) SetDefault(context.Context, audio.Kind, audio.Target) error { return nil }

func testConfig() config.Config {
	cfg := config.Defaults()
	cfg.Audio.HeadsetSink = "headset"
	cfg.Audio.FallbackSink = "speakers"
	return cfg
}

func await(t *testing.T, channel <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func decodeRecords(t *testing.T, output string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("invalid JSON line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func countEvents(records []map[string]any, event string) int {
	count := 0
	for _, record := range records {
		if record["event"] == event {
			count++
		}
	}
	return count
}
