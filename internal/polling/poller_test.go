package polling

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dudeofawesome/pslinkd/internal/discovery"
	"github.com/dudeofawesome/pslinkd/internal/hid"
	"github.com/dudeofawesome/pslinkd/internal/state"
)

type fakeTicker struct {
	ticks chan time.Time
}

func (ticker *fakeTicker) C() <-chan time.Time { return ticker.ticks }
func (*fakeTicker) Stop()                      {}

type fakeClock struct {
	ticker *fakeTicker
}

func (clock *fakeClock) NewTicker(time.Duration) Ticker { return clock.ticker }

type scriptedResult struct {
	data []byte
	err  error
}

type scriptedReader struct {
	mu      sync.Mutex
	results []scriptedResult
	closed  chan struct{}
}

func (reader *scriptedReader) ReadFeature() ([]byte, error) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if len(reader.results) == 0 {
		return nil, errors.New("script exhausted")
	}
	result := reader.results[0]
	reader.results = reader.results[1:]
	return result.data, result.err
}

func (reader *scriptedReader) Close() error {
	select {
	case <-reader.closed:
	default:
		close(reader.closed)
	}
	return nil
}

type recordingObserver struct {
	connections    chan state.Connection
	interactions   chan state.InteractionUpdate
	invalidVolumes chan uint8
	failures       chan error
	recoveries     chan string
}

func (observer *recordingObserver) ConnectionChanged(connection state.Connection) {
	observer.connections <- connection
}

func (observer *recordingObserver) InteractionChanged(update state.InteractionUpdate) {
	observer.interactions <- update
}

func (observer *recordingObserver) InvalidVolume(_ string, value uint8) {
	observer.invalidVolumes <- value
}

func (observer *recordingObserver) HIDFailure(_ string, err error) {
	observer.failures <- err
}

func (observer *recordingObserver) HIDRecovered(path string) {
	observer.recoveries <- path
}

type harness struct {
	clock      *fakeClock
	selections chan discovery.Candidate
	observer   *recordingObserver
	readers    map[string]*scriptedReader
	cancel     context.CancelFunc
	done       chan error
}

func newHarness(t *testing.T, readers map[string]*scriptedReader) *harness {
	t.Helper()
	clock := &fakeClock{ticker: &fakeTicker{ticks: make(chan time.Time)}}
	observer := &recordingObserver{
		connections:    make(chan state.Connection, 20),
		interactions:   make(chan state.InteractionUpdate, 20),
		invalidVolumes: make(chan uint8, 20),
		failures:       make(chan error, 20),
		recoveries:     make(chan string, 20),
	}
	h := &harness{
		clock:      clock,
		selections: make(chan discovery.Candidate),
		observer:   observer,
		readers:    readers,
		done:       make(chan error, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	poller := New(200*time.Millisecond, 3, clock, func(path string) (Reader, error) {
		reader, found := readers[path]
		if !found {
			return nil, errors.New("open failed")
		}
		return reader, nil
	}, observer)
	go func() { h.done <- poller.Run(ctx, h.selections) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-h.done:
			if err != nil {
				t.Errorf("poller stopped with %v", err)
			}
		case <-time.After(time.Second):
			t.Error("poller did not stop")
		}
	})
	return h
}

func featureReport(connected bool) []byte {
	report := make([]byte, hid.ReportLength)
	report[0] = hid.ReportID
	if connected {
		report[39] = 1
	}
	return report
}

func newScriptedReader(results ...scriptedResult) *scriptedReader {
	return &scriptedReader{results: results, closed: make(chan struct{})}
}

func (h *harness) selectDevice(candidate discovery.Candidate) {
	h.selections <- candidate
}

func (h *harness) tick() {
	h.clock.ticker.ticks <- time.Time{}
}

func receiveConnection(t *testing.T, observer *recordingObserver) state.Connection {
	t.Helper()
	select {
	case connection := <-observer.connections:
		return connection
	case <-time.After(time.Second):
		t.Fatal("no connection transition")
		return state.Connection{}
	}
}

func assertNoConnection(t *testing.T, observer *recordingObserver) {
	t.Helper()
	select {
	case connection := <-observer.connections:
		t.Fatalf("unexpected connection transition: %#v", connection)
	default:
	}
}

func assertNoHIDNotification(t *testing.T, observer *recordingObserver) {
	t.Helper()
	select {
	case err := <-observer.failures:
		t.Fatalf("unexpected HID failure: %v", err)
	case path := <-observer.recoveries:
		t.Fatalf("unexpected HID recovery for %q", path)
	default:
	}
}

func receiveInteraction(t *testing.T, observer *recordingObserver) state.InteractionUpdate {
	t.Helper()
	select {
	case update := <-observer.interactions:
		return update
	case <-time.After(time.Second):
		t.Fatal("no interaction update")
		return state.InteractionUpdate{}
	}
}

func TestStartupWithoutAdapterRequestsFallback(t *testing.T) {
	h := newHarness(t, nil)
	h.selectDevice(discovery.Candidate{})
	if got := receiveConnection(t, h.observer); got != (state.Connection{}) {
		t.Fatalf("connection = %#v", got)
	}
}

func TestImmediateConnectionAndThreeFailureDisconnect(t *testing.T) {
	readError := errors.New("feature read failed")
	reader := newScriptedReader(
		scriptedResult{data: featureReport(true)},
		scriptedResult{err: readError},
		scriptedResult{data: featureReport(false)},
		scriptedResult{err: readError},
	)
	h := newHarness(t, map[string]*scriptedReader{"/dev/hidraw1": reader})
	h.selectDevice(discovery.Candidate{Syspath: "/sys/a", Devnode: "/dev/hidraw1"})

	h.tick()
	if got := receiveConnection(t, h.observer); !got.AdapterPresent || !got.HeadsetConnected {
		t.Fatalf("connection = %#v", got)
	}
	h.tick()
	<-h.observer.failures
	assertNoConnection(t, h.observer)
	// A valid report with a clear connection bit is a failure sample, but not
	// an I/O error, so it deliberately does not notify HIDFailure.
	h.tick()
	h.tick()
	<-h.observer.failures
	if got := receiveConnection(t, h.observer); !got.AdapterPresent || got.HeadsetConnected {
		t.Fatalf("disconnection = %#v", got)
	}
}

func TestSuccessfulConnectionResetsFailureCount(t *testing.T) {
	readError := errors.New("feature read failed")
	reader := newScriptedReader(
		scriptedResult{data: featureReport(true)},
		scriptedResult{err: readError},
		scriptedResult{err: readError},
		scriptedResult{data: featureReport(true)},
		scriptedResult{err: readError},
	)
	h := newHarness(t, map[string]*scriptedReader{"/dev/hidraw1": reader})
	h.selectDevice(discovery.Candidate{Syspath: "/sys/a", Devnode: "/dev/hidraw1"})
	h.tick()
	receiveConnection(t, h.observer)
	for range 2 {
		h.tick()
		<-h.observer.failures
	}
	h.tick()
	if recovered := <-h.observer.recoveries; recovered != "/dev/hidraw1" {
		t.Fatalf("recovered path = %q", recovered)
	}
	h.tick()
	<-h.observer.failures
	assertNoConnection(t, h.observer)
}

func TestPoweredOffAdapterRequestsFallbackAtThreshold(t *testing.T) {
	reader := newScriptedReader(
		scriptedResult{data: featureReport(false)},
		scriptedResult{data: featureReport(false)},
		scriptedResult{data: featureReport(false)},
	)
	h := newHarness(t, map[string]*scriptedReader{"/dev/hidraw1": reader})
	h.selectDevice(discovery.Candidate{Syspath: "/sys/a", Devnode: "/dev/hidraw1"})
	h.tick()
	h.tick()
	assertNoConnection(t, h.observer)
	h.tick()
	if got := receiveConnection(t, h.observer); !got.AdapterPresent || got.HeadsetConnected {
		t.Fatalf("powered-off state = %#v", got)
	}
}

func TestExpectedReadErrorsDisconnectWithoutFailureOrRecoveryNotifications(t *testing.T) {
	brokenPipe := fmt.Errorf("HIDIOCGFEATURE report 0xb0: %w", syscall.EPIPE)
	reader := newScriptedReader(
		scriptedResult{err: brokenPipe},
		scriptedResult{err: brokenPipe},
		scriptedResult{err: brokenPipe},
		scriptedResult{data: featureReport(true)},
	)
	h := newHarness(t, map[string]*scriptedReader{"/dev/hidraw1": reader})
	h.selectDevice(discovery.Candidate{Syspath: "/sys/a", Devnode: "/dev/hidraw1"})

	for range 2 {
		h.tick()
		assertNoConnection(t, h.observer)
		assertNoHIDNotification(t, h.observer)
	}
	h.tick()
	if got := receiveConnection(t, h.observer); !got.AdapterPresent || got.HeadsetConnected {
		t.Fatalf("powered-off state = %#v", got)
	}
	assertNoHIDNotification(t, h.observer)

	h.tick()
	if got := receiveConnection(t, h.observer); !got.AdapterPresent || !got.HeadsetConnected {
		t.Fatalf("reconnected state = %#v", got)
	}
	assertNoHIDNotification(t, h.observer)
}

func TestPollerNormalizesButtonsAndResetsBaselineOnReplacement(t *testing.T) {
	baseline := featureReport(true)
	baseline[39] |= 0x08
	baseline[43] = 0xf0
	baseline[44] = 3
	released := featureReport(true)
	released[43] = 0xf0
	released[44] = 3
	pressed := append([]byte(nil), released...)
	pressed[39] |= 0x38

	first := newScriptedReader(
		scriptedResult{data: baseline},
		scriptedResult{data: released},
		scriptedResult{data: pressed},
	)
	second := newScriptedReader(scriptedResult{data: pressed})
	h := newHarness(t, map[string]*scriptedReader{
		"/dev/hidraw1": first,
		"/dev/hidraw2": second,
	})
	h.selectDevice(discovery.Candidate{Syspath: "/sys/a", Devnode: "/dev/hidraw1"})
	h.tick()
	receiveConnection(t, h.observer)
	initial := receiveInteraction(t, h.observer)
	if initial.VolumeUpPressed || !initial.VolumeChanged || initial.Volume.Value != 3 ||
		!initial.MicrophoneChanged || initial.MicrophoneMuted.Value {
		t.Fatalf("initial interaction = %#v", initial)
	}
	h.tick()
	h.tick()
	edges := receiveInteraction(t, h.observer)
	if !edges.VolumeUpPressed || !edges.VolumeDownPressed || !edges.MicrophoneMutePressed {
		t.Fatalf("button edges = %#v", edges)
	}

	h.selectDevice(discovery.Candidate{Syspath: "/sys/b", Devnode: "/dev/hidraw2"})
	receiveConnection(t, h.observer)
	h.tick()
	receiveConnection(t, h.observer)
	replacement := receiveInteraction(t, h.observer)
	if replacement.VolumeUpPressed || replacement.VolumeDownPressed ||
		replacement.MicrophoneMutePressed {
		t.Fatalf("replacement baseline synthesized presses: %#v", replacement)
	}
}

func TestPollerReportsAndOmitsInvalidVolume(t *testing.T) {
	report := featureReport(true)
	report[44] = 16
	reader := newScriptedReader(scriptedResult{data: report})
	h := newHarness(t, map[string]*scriptedReader{"/dev/hidraw1": reader})
	h.selectDevice(discovery.Candidate{Syspath: "/sys/a", Devnode: "/dev/hidraw1"})
	h.tick()
	receiveConnection(t, h.observer)
	if got := <-h.observer.invalidVolumes; got != 16 {
		t.Fatalf("invalid volume = %d", got)
	}
	if update := receiveInteraction(t, h.observer); update.Volume.Valid {
		t.Fatalf("invalid volume entered normalized state: %#v", update)
	}
}

func TestOpenFailuresUseDisconnectThreshold(t *testing.T) {
	h := newHarness(t, nil)
	h.selectDevice(discovery.Candidate{Syspath: "/sys/a", Devnode: "/dev/hidraw1"})
	for range 2 {
		h.tick()
		<-h.observer.failures
		assertNoConnection(t, h.observer)
	}
	h.tick()
	<-h.observer.failures
	if got := receiveConnection(t, h.observer); !got.AdapterPresent || got.HeadsetConnected {
		t.Fatalf("open-failure state = %#v", got)
	}
}

func TestReplugClosesOldReaderAndPollsNewPath(t *testing.T) {
	first := newScriptedReader(scriptedResult{data: featureReport(true)})
	second := newScriptedReader(scriptedResult{data: featureReport(true)})
	h := newHarness(t, map[string]*scriptedReader{
		"/dev/hidraw1": first,
		"/dev/hidraw7": second,
	})
	h.selectDevice(discovery.Candidate{Syspath: "/sys/a", Devnode: "/dev/hidraw1"})
	h.tick()
	receiveConnection(t, h.observer)
	h.selectDevice(discovery.Candidate{Syspath: "/sys/b", Devnode: "/dev/hidraw7"})
	if got := receiveConnection(t, h.observer); got != (state.Connection{}) {
		t.Fatalf("replacement removal = %#v", got)
	}
	select {
	case <-first.closed:
	default:
		t.Fatal("old reader was not closed")
	}
	h.tick()
	if got := receiveConnection(t, h.observer); !reflect.DeepEqual(got, state.Connection{AdapterPresent: true, HeadsetConnected: true}) {
		t.Fatalf("new connection = %#v", got)
	}
}

type blockingReader struct {
	started chan struct{}
	closed  chan struct{}
}

func (reader *blockingReader) ReadFeature() ([]byte, error) {
	close(reader.started)
	<-reader.closed
	return nil, errors.New("closed")
}

func (reader *blockingReader) Close() error {
	select {
	case <-reader.closed:
	default:
		close(reader.closed)
	}
	return nil
}

func TestCancellationClosesInFlightReaderPromptly(t *testing.T) {
	reader := &blockingReader{started: make(chan struct{}), closed: make(chan struct{})}
	clock := &fakeClock{ticker: &fakeTicker{ticks: make(chan time.Time)}}
	observer := &recordingObserver{
		connections:    make(chan state.Connection, 1),
		interactions:   make(chan state.InteractionUpdate, 1),
		invalidVolumes: make(chan uint8, 1),
		failures:       make(chan error, 1),
		recoveries:     make(chan string, 1),
	}
	selections := make(chan discovery.Candidate)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	poller := New(time.Second, 3, clock, func(string) (Reader, error) { return reader, nil }, observer)
	go func() { done <- poller.Run(ctx, selections) }()
	selections <- discovery.Candidate{Syspath: "/sys/a", Devnode: "/dev/hidraw1"}
	clock.ticker.ticks <- time.Time{}
	<-reader.started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("poller did not cancel while read was in flight")
	}
	select {
	case <-reader.closed:
	default:
		t.Fatal("reader was not closed on cancellation")
	}
}
