package audio

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

type setCall struct {
	kind   Kind
	target string
}

type fakeSetter struct {
	calls   chan setCall
	results chan error
}

func (setter *fakeSetter) SetDefault(ctx context.Context, kind Kind, target string) error {
	select {
	case setter.calls <- setCall{kind: kind, target: target}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-setter.results:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type fakeTimer struct {
	ticks   chan time.Time
	stopped bool
	mu      sync.Mutex
}

func (timer *fakeTimer) C() <-chan time.Time { return timer.ticks }
func (timer *fakeTimer) Stop() bool {
	timer.mu.Lock()
	defer timer.mu.Unlock()
	wasActive := !timer.stopped
	timer.stopped = true
	return wasActive
}

type timerRequest struct {
	delay time.Duration
	timer *fakeTimer
}

type fakeRetryClock struct {
	timers chan timerRequest
}

func (clock *fakeRetryClock) NewTimer(delay time.Duration) Timer {
	timer := &fakeTimer{ticks: make(chan time.Time, 1)}
	clock.timers <- timerRequest{delay: delay, timer: timer}
	return timer
}

type actionEvent struct {
	revision uint64
	desired  Desired
	attempt  int
	err      error
}

type fakeActionObserver struct {
	succeeded chan actionEvent
	retrying  chan actionEvent
}

func (observer *fakeActionObserver) AudioActionSucceeded(revision uint64, desired Desired, attempt int) {
	observer.succeeded <- actionEvent{revision: revision, desired: desired, attempt: attempt}
}

func (observer *fakeActionObserver) AudioActionRetrying(
	revision uint64,
	desired Desired,
	attempt int,
	err error,
) {
	observer.retrying <- actionEvent{revision: revision, desired: desired, attempt: attempt, err: err}
}

type controllerHarness struct {
	setter   *fakeSetter
	clock    *fakeRetryClock
	observer *fakeActionObserver
	desired  chan Desired
	cancel   context.CancelFunc
	done     chan error
}

func newControllerHarness(t *testing.T, targets Targets) *controllerHarness {
	t.Helper()
	harness := &controllerHarness{
		setter: &fakeSetter{
			calls:   make(chan setCall, 20),
			results: make(chan error, 20),
		},
		clock: &fakeRetryClock{timers: make(chan timerRequest, 20)},
		observer: &fakeActionObserver{
			succeeded: make(chan actionEvent, 20),
			retrying:  make(chan actionEvent, 20),
		},
		desired: make(chan Desired),
		done:    make(chan error, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	harness.cancel = cancel
	controller := NewController(harness.setter, targets, harness.clock, harness.observer)
	go func() { harness.done <- controller.Run(ctx, harness.desired) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-harness.done:
			if err != nil {
				t.Errorf("controller stopped with %v", err)
			}
		case <-time.After(time.Second):
			t.Error("controller did not stop")
		}
	})
	return harness
}

func receiveSetCall(t *testing.T, setter *fakeSetter) setCall {
	t.Helper()
	select {
	case call := <-setter.calls:
		return call
	case <-time.After(time.Second):
		t.Fatal("no audio action")
		return setCall{}
	}
}

func receiveSucceeded(t *testing.T, observer *fakeActionObserver) actionEvent {
	t.Helper()
	select {
	case event := <-observer.succeeded:
		return event
	case <-time.After(time.Second):
		t.Fatal("no successful action event")
		return actionEvent{}
	}
}

func receiveRetry(t *testing.T, observer *fakeActionObserver) actionEvent {
	t.Helper()
	select {
	case event := <-observer.retrying:
		return event
	case <-time.After(time.Second):
		t.Fatal("no retry event")
		return actionEvent{}
	}
}

func TestControllerRoutesHeadsetAndFallbackSink(t *testing.T) {
	h := newControllerHarness(t, Targets{HeadsetSink: "headset", FallbackSink: "speakers"})

	h.desired <- Desired{HeadsetConnected: true}
	if got := receiveSetCall(t, h.setter); got != (setCall{kind: Sink, target: "headset"}) {
		t.Fatalf("headset call = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)

	h.desired <- Desired{}
	if got := receiveSetCall(t, h.setter); got != (setCall{kind: Sink, target: "speakers"}) {
		t.Fatalf("fallback call = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)
}

func TestControllerRoutesOptionalSourcePair(t *testing.T) {
	h := newControllerHarness(t, Targets{
		HeadsetSink:    "headset",
		FallbackSink:   "speakers",
		HeadsetSource:  "headset-mic",
		FallbackSource: "desk-mic",
	})
	h.desired <- Desired{HeadsetConnected: true}

	if got := receiveSetCall(t, h.setter); got != (setCall{kind: Sink, target: "headset"}) {
		t.Fatalf("sink call = %#v", got)
	}
	h.setter.results <- nil
	if got := receiveSetCall(t, h.setter); got != (setCall{kind: Source, target: "headset-mic"}) {
		t.Fatalf("source call = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)

	h.desired <- Desired{}
	if got := receiveSetCall(t, h.setter); got != (setCall{kind: Sink, target: "speakers"}) {
		t.Fatalf("fallback sink call = %#v", got)
	}
	h.setter.results <- nil
	if got := receiveSetCall(t, h.setter); got != (setCall{kind: Source, target: "desk-mic"}) {
		t.Fatalf("fallback source call = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)
}

func TestControllerRetriesWithBackoffAndRecovers(t *testing.T) {
	h := newControllerHarness(t, Targets{HeadsetSink: "headset", FallbackSink: "speakers"})
	h.desired <- Desired{HeadsetConnected: true}
	receiveSetCall(t, h.setter)
	h.setter.results <- errors.New("wireplumber unavailable")
	if event := receiveRetry(t, h.observer); event.attempt != 1 || event.err == nil {
		t.Fatalf("retry event = %#v", event)
	}

	timer := <-h.clock.timers
	if timer.delay != 250*time.Millisecond {
		t.Fatalf("first delay = %s", timer.delay)
	}
	timer.timer.ticks <- time.Time{}
	receiveSetCall(t, h.setter)
	h.setter.results <- errors.New("still unavailable")
	receiveRetry(t, h.observer)
	secondTimer := <-h.clock.timers
	if secondTimer.delay != 500*time.Millisecond {
		t.Fatalf("second delay = %s", secondTimer.delay)
	}
	secondTimer.timer.ticks <- time.Time{}
	receiveSetCall(t, h.setter)
	h.setter.results <- nil
	if event := receiveSucceeded(t, h.observer); event.attempt != 3 {
		t.Fatalf("success event = %#v", event)
	}
}

func TestControllerCancelsObsoleteAction(t *testing.T) {
	h := newControllerHarness(t, Targets{HeadsetSink: "headset", FallbackSink: "speakers"})
	h.desired <- Desired{HeadsetConnected: true}
	if got := receiveSetCall(t, h.setter); got.target != "headset" {
		t.Fatalf("first call = %#v", got)
	}

	h.desired <- Desired{}
	if got := receiveSetCall(t, h.setter); got.target != "speakers" {
		t.Fatalf("replacement call = %#v", got)
	}
	h.setter.results <- nil
	event := receiveSucceeded(t, h.observer)
	if event.desired.HeadsetConnected {
		t.Fatalf("obsolete revision succeeded: %#v", event)
	}
	select {
	case retry := <-h.observer.retrying:
		t.Fatalf("obsolete revision retried: %#v", retry)
	default:
	}
}

func TestControllerCancelsObsoleteRetryTimer(t *testing.T) {
	h := newControllerHarness(t, Targets{HeadsetSink: "headset", FallbackSink: "speakers"})
	h.desired <- Desired{HeadsetConnected: true}
	receiveSetCall(t, h.setter)
	h.setter.results <- errors.New("wireplumber unavailable")
	receiveRetry(t, h.observer)
	timer := <-h.clock.timers

	h.desired <- Desired{}
	if got := receiveSetCall(t, h.setter); got.target != "speakers" {
		t.Fatalf("replacement call = %#v", got)
	}
	timer.timer.mu.Lock()
	stopped := timer.timer.stopped
	timer.timer.mu.Unlock()
	if !stopped {
		t.Fatal("obsolete retry timer was not stopped")
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)

	timer.timer.ticks <- time.Time{}
	select {
	case call := <-h.setter.calls:
		t.Fatalf("obsolete revision retried after cancellation: %#v", call)
	default:
	}
}

func TestControllerDoesNotRepeatSuccessfulActionWithoutTransition(t *testing.T) {
	h := newControllerHarness(t, Targets{HeadsetSink: "headset", FallbackSink: "speakers"})
	h.desired <- Desired{HeadsetConnected: true}
	receiveSetCall(t, h.setter)
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)

	select {
	case call := <-h.setter.calls:
		t.Fatalf("successful action was repeated: %#v", call)
	case timer := <-h.clock.timers:
		t.Fatalf("successful action scheduled an audit: %#v", timer)
	default:
	}
}

func TestRetryDelayIsBounded(t *testing.T) {
	controller := NewController(nil, Targets{}, nil, nil)
	got := []time.Duration{
		controller.retryDelay(1),
		controller.retryDelay(2),
		controller.retryDelay(8),
		controller.retryDelay(100),
	}
	want := []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, 30 * time.Second, 30 * time.Second}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delays = %v, want %v", got, want)
	}
}
