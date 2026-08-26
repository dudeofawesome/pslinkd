package polling

import (
	"context"
	"errors"
	"reflect"
	"sync"
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
	connections chan state.Connection
	failures    chan error
}

func (observer *recordingObserver) ConnectionChanged(connection state.Connection) {
	observer.connections <- connection
}

func (observer *recordingObserver) HIDFailure(_ string, err error) {
	observer.failures <- err
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
		connections: make(chan state.Connection, 20),
		failures:    make(chan error, 20),
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
		connections: make(chan state.Connection, 1),
		failures:    make(chan error, 1),
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
