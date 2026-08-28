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
	op     string
	kind   Kind
	target Target
	volume uint8
	scalar float64
	muted  bool
}

type fakeSetter struct {
	calls       chan setCall
	results     chan error
	volumeReads chan float64
}

func (setter *fakeSetter) SetDefault(ctx context.Context, kind Kind, target Target) error {
	return setter.call(ctx, setCall{kind: kind, target: target})
}

func (setter *fakeSetter) SetVolume(ctx context.Context, target Target, volume uint8) error {
	return setter.call(ctx, setCall{op: "volume", kind: Sink, target: target, volume: volume})
}

func (setter *fakeSetter) SetMute(ctx context.Context, target Target, muted bool) error {
	return setter.call(ctx, setCall{op: "mute", kind: Source, target: target, muted: muted})
}

func (setter *fakeSetter) GetVolume(ctx context.Context, target Target) (float64, error) {
	if err := setter.call(ctx, setCall{op: "get-volume", kind: Sink, target: target}); err != nil {
		return 0, err
	}
	select {
	case volume := <-setter.volumeReads:
		return volume, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func (setter *fakeSetter) SetVolumeScalar(
	ctx context.Context,
	target Target,
	volume float64,
) error {
	return setter.call(ctx, setCall{op: "set-volume-scalar", kind: Sink, target: target, scalar: volume})
}

func (setter *fakeSetter) call(ctx context.Context, call setCall) error {
	select {
	case setter.calls <- call:
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
			calls:       make(chan setCall, 20),
			results:     make(chan error, 20),
			volumeReads: make(chan float64, 20),
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
	if got := receiveSetCall(t, h.setter); got != (setCall{kind: Sink, target: Target{Name: "headset"}}) {
		t.Fatalf("headset call = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)

	h.desired <- Desired{}
	if got := receiveSetCall(t, h.setter); got != (setCall{kind: Sink, target: Target{Name: "speakers"}}) {
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

	if got := receiveSetCall(t, h.setter); got != (setCall{kind: Sink, target: Target{Name: "headset"}}) {
		t.Fatalf("sink call = %#v", got)
	}
	h.setter.results <- nil
	if got := receiveSetCall(t, h.setter); got != (setCall{kind: Source, target: Target{Name: "headset-mic"}}) {
		t.Fatalf("source call = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)

	h.desired <- Desired{}
	if got := receiveSetCall(t, h.setter); got != (setCall{kind: Sink, target: Target{Name: "speakers"}}) {
		t.Fatalf("fallback sink call = %#v", got)
	}
	h.setter.results <- nil
	if got := receiveSetCall(t, h.setter); got != (setCall{kind: Source, target: Target{Name: "desk-mic"}}) {
		t.Fatalf("fallback source call = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)
}

func TestControllerAutomaticallyRoutesHeadsetTargetsForSelectedUSB(t *testing.T) {
	h := newControllerHarness(t, Targets{
		FallbackSink:   "speakers",
		FallbackSource: "desk-mic",
	})
	usb := USBIdentity{Syspath: "/sys/usb/selected", Serial: "serial-a"}
	h.desired <- Desired{HeadsetConnected: true, USB: usb}

	if got := receiveSetCall(t, h.setter); got != (setCall{
		kind: Sink, target: Target{USB: usb},
	}) {
		t.Fatalf("automatic sink call = %#v", got)
	}
	h.setter.results <- nil
	if got := receiveSetCall(t, h.setter); got != (setCall{
		kind: Source, target: Target{USB: usb},
	}) {
		t.Fatalf("automatic source call = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)
}

func TestControllerUsesReplacementAdapterIdentityAfterReplug(t *testing.T) {
	h := newControllerHarness(t, Targets{FallbackSink: "speakers"})
	first := USBIdentity{Syspath: "/sys/usb/first", Serial: "first"}
	second := USBIdentity{Syspath: "/sys/usb/second", Serial: "second"}

	h.desired <- Desired{HeadsetConnected: true, USB: first}
	if got := receiveSetCall(t, h.setter); got.target.USB != first {
		t.Fatalf("first adapter target = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)

	h.desired <- Desired{}
	if got := receiveSetCall(t, h.setter); got.target.Name != "speakers" {
		t.Fatalf("removal fallback target = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)

	h.desired <- Desired{HeadsetConnected: true, USB: second}
	if got := receiveSetCall(t, h.setter); got.target.USB != second {
		t.Fatalf("replacement adapter target = %#v", got)
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
	if got := receiveSetCall(t, h.setter); got.target.Name != "headset" {
		t.Fatalf("first call = %#v", got)
	}

	h.desired <- Desired{}
	if got := receiveSetCall(t, h.setter); got.target.Name != "speakers" {
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
	if got := receiveSetCall(t, h.setter); got.target.Name != "speakers" {
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

func TestControllerConvergesInitialAndChangedControlsWithoutReassertingRoute(t *testing.T) {
	h := newControllerHarness(t, Targets{
		HeadsetSink:    "headset",
		FallbackSink:   "speakers",
		HeadsetSource:  "headset-mic",
		FallbackSource: "desk-mic",
	})
	desired := Desired{
		HeadsetConnected: true,
		ControlsEnabled:  true,
		VolumeMode:       "synchronized",
		Volume:           OptionalVolume{Valid: true, Value: 6},
		MicrophoneMuted:  OptionalBool{Valid: true, Value: true},
	}
	h.desired <- desired
	for _, want := range []setCall{
		{kind: Sink, target: Target{Name: "headset"}},
		{kind: Source, target: Target{Name: "headset-mic"}},
		{op: "volume", kind: Sink, target: Target{Name: "headset"}, volume: 6},
		{op: "mute", kind: Source, target: Target{Name: "headset-mic"}, muted: true},
	} {
		if got := receiveSetCall(t, h.setter); got != want {
			t.Fatalf("initial control call = %#v, want %#v", got, want)
		}
		h.setter.results <- nil
	}
	receiveSucceeded(t, h.observer)

	desired.Volume.Value = 7
	h.desired <- desired
	if got := receiveSetCall(t, h.setter); got != (setCall{
		op: "volume", kind: Sink, target: Target{Name: "headset"}, volume: 7,
	}) {
		t.Fatalf("changed volume call = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)
	select {
	case call := <-h.setter.calls:
		t.Fatalf("control change reasserted another action: %#v", call)
	default:
	}
}

func TestControllerSkipsControlsWhenDisabledOrDisconnected(t *testing.T) {
	h := newControllerHarness(t, Targets{
		HeadsetSink:    "headset",
		FallbackSink:   "speakers",
		HeadsetSource:  "headset-mic",
		FallbackSource: "desk-mic",
	})
	h.desired <- Desired{
		HeadsetConnected: true,
		Volume:           OptionalVolume{Valid: true, Value: 15},
		MicrophoneMuted:  OptionalBool{Valid: true, Value: true},
	}
	for range 2 {
		call := receiveSetCall(t, h.setter)
		if call.op != "" {
			t.Fatalf("disabled control call = %#v", call)
		}
		h.setter.results <- nil
	}
	receiveSucceeded(t, h.observer)

	h.desired <- Desired{
		ControlsEnabled: true,
		Volume:          OptionalVolume{Valid: true, Value: 9},
		MicrophoneMuted: OptionalBool{Valid: true, Value: true},
	}
	for range 2 {
		call := receiveSetCall(t, h.setter)
		if call.op != "" {
			t.Fatalf("disconnected stale control call = %#v", call)
		}
		h.setter.results <- nil
	}
	receiveSucceeded(t, h.observer)
}

func TestControllerSkipsMuteWithoutSourceRouting(t *testing.T) {
	h := newControllerHarness(t, Targets{HeadsetSink: "headset", FallbackSink: "speakers"})
	h.desired <- Desired{
		HeadsetConnected: true,
		ControlsEnabled:  true,
		MicrophoneMuted:  OptionalBool{Valid: true, Value: true},
	}
	if got := receiveSetCall(t, h.setter); got.op != "" || got.kind != Sink {
		t.Fatalf("route call = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)
	select {
	case call := <-h.setter.calls:
		t.Fatalf("mute action without source routing = %#v", call)
	default:
	}
}

func TestControllerHostOnlyUsesFreshReadAndAbsoluteIdempotentRetry(t *testing.T) {
	h := newControllerHarness(t, Targets{HeadsetSink: "headset", FallbackSink: "speakers"})
	desired := Desired{
		HeadsetConnected: true,
		ControlsEnabled:  true,
		VolumeMode:       "host-only",
		HostVolumeSteps:  1,
	}
	h.desired <- desired
	receiveSetCall(t, h.setter)
	h.setter.results <- nil
	if got := receiveSetCall(t, h.setter); got.op != "get-volume" {
		t.Fatalf("first host action = %#v", got)
	}
	h.setter.results <- nil
	h.setter.volumeReads <- 0.5
	want := 0.5 + 1.0/15
	if got := receiveSetCall(t, h.setter); got.op != "set-volume-scalar" || got.scalar != want {
		t.Fatalf("absolute host action = %#v, want %g", got, want)
	}
	h.setter.results <- errors.New("temporary failure")
	receiveRetry(t, h.observer)
	timer := <-h.clock.timers
	timer.timer.ticks <- time.Time{}
	receiveSetCall(t, h.setter) // routing retry
	h.setter.results <- nil
	if got := receiveSetCall(t, h.setter); got.op != "set-volume-scalar" || got.scalar != want {
		t.Fatalf("idempotent retry = %#v, want %g", got, want)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)

	desired.HostVolumeSteps++
	h.desired <- desired
	if got := receiveSetCall(t, h.setter); got.op != "get-volume" {
		t.Fatalf("next edge did not refresh volume: %#v", got)
	}
	h.setter.results <- nil
	h.setter.volumeReads <- 0.25
	if got := receiveSetCall(t, h.setter); got.op != "set-volume-scalar" || got.scalar != 0.25+1.0/15 {
		t.Fatalf("second absolute host action = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)
}

func TestControllerHostOnlyClampsAndIgnoresAbsoluteReports(t *testing.T) {
	h := newControllerHarness(t, Targets{HeadsetSink: "headset", FallbackSink: "speakers"})
	h.desired <- Desired{
		HeadsetConnected: true,
		ControlsEnabled:  true,
		VolumeMode:       "host-only",
		HostVolumeSteps:  -1,
		Volume:           OptionalVolume{Valid: true, Value: 7},
	}
	receiveSetCall(t, h.setter)
	h.setter.results <- nil
	if got := receiveSetCall(t, h.setter); got.op != "get-volume" {
		t.Fatalf("host read = %#v", got)
	}
	h.setter.results <- nil
	h.setter.volumeReads <- 0.01
	if got := receiveSetCall(t, h.setter); got.op != "set-volume-scalar" || got.scalar != 0 {
		t.Fatalf("clamped host action = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)
}

func TestControllerHostOnlyAccumulatesEdgesAgainstPendingTarget(t *testing.T) {
	h := newControllerHarness(t, Targets{HeadsetSink: "headset", FallbackSink: "speakers"})
	desired := Desired{
		HeadsetConnected: true,
		ControlsEnabled:  true,
		VolumeMode:       "host-only",
		HostVolumeSteps:  1,
	}
	h.desired <- desired
	receiveSetCall(t, h.setter)
	h.setter.results <- nil
	if got := receiveSetCall(t, h.setter); got.op != "get-volume" {
		t.Fatalf("host read = %#v", got)
	}
	h.setter.results <- nil
	h.setter.volumeReads <- 0.5
	first := receiveSetCall(t, h.setter)
	if first.op != "set-volume-scalar" {
		t.Fatalf("first pending write = %#v", first)
	}

	desired.HostVolumeSteps = 2
	h.desired <- desired
	// The obsolete scalar write is canceled. Routing has not completed as a
	// revision yet, so it is safely retried before the accumulated target.
	receiveSetCall(t, h.setter)
	h.setter.results <- nil
	got := receiveSetCall(t, h.setter)
	want := 0.5 + 2.0/15
	if got.op != "set-volume-scalar" || got.scalar != want {
		t.Fatalf("accumulated write = %#v, want %g", got, want)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)
	select {
	case call := <-h.setter.calls:
		t.Fatalf("pending accumulation performed a stale read: %#v", call)
	default:
	}
}

func TestControllerHostOnlyDoesNothingWhileControlsDisabled(t *testing.T) {
	h := newControllerHarness(t, Targets{HeadsetSink: "headset", FallbackSink: "speakers"})
	h.desired <- Desired{
		HeadsetConnected: true,
		VolumeMode:       "host-only",
		HostVolumeSteps:  1,
	}
	if got := receiveSetCall(t, h.setter); got.op != "" {
		t.Fatalf("disabled host-volume action = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)
	select {
	case call := <-h.setter.calls:
		t.Fatalf("disabled controls produced action %#v", call)
	default:
	}
}

func TestControllerHostOnlyDoesNotActOnRawVolumeAndKeepsMuteConvergence(t *testing.T) {
	h := newControllerHarness(t, Targets{
		HeadsetSink:    "headset",
		FallbackSink:   "speakers",
		HeadsetSource:  "headset-mic",
		FallbackSource: "desk-mic",
	})
	desired := Desired{
		HeadsetConnected: true,
		ControlsEnabled:  true,
		VolumeMode:       "host-only",
		Volume:           OptionalVolume{Valid: true, Value: 3},
		MicrophoneMuted:  OptionalBool{Valid: true, Value: true},
	}
	h.desired <- desired
	for _, want := range []setCall{
		{kind: Sink, target: Target{Name: "headset"}},
		{kind: Source, target: Target{Name: "headset-mic"}},
		{op: "mute", kind: Source, target: Target{Name: "headset-mic"}, muted: true},
	} {
		if got := receiveSetCall(t, h.setter); got != want {
			t.Fatalf("host-only initial action = %#v, want %#v", got, want)
		}
		h.setter.results <- nil
	}
	receiveSucceeded(t, h.observer)
	desired.Volume.Value = 9
	h.desired <- desired
	select {
	case call := <-h.setter.calls:
		t.Fatalf("raw absolute volume caused host action: %#v", call)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestControllerHostOnlyClampsAtUpperBound(t *testing.T) {
	h := newControllerHarness(t, Targets{HeadsetSink: "headset", FallbackSink: "speakers"})
	h.desired <- Desired{
		HeadsetConnected: true,
		ControlsEnabled:  true,
		VolumeMode:       "host-only",
		HostVolumeSteps:  1,
	}
	receiveSetCall(t, h.setter)
	h.setter.results <- nil
	receiveSetCall(t, h.setter)
	h.setter.results <- nil
	h.setter.volumeReads <- 0.99
	if got := receiveSetCall(t, h.setter); got.scalar != 1 {
		t.Fatalf("upper-clamped host action = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)
}

func TestControllerRetriesControlAndCancelsItOnDisconnect(t *testing.T) {
	h := newControllerHarness(t, Targets{HeadsetSink: "headset", FallbackSink: "speakers"})
	connected := Desired{
		HeadsetConnected: true,
		ControlsEnabled:  true,
		Volume:           OptionalVolume{Valid: true, Value: 4},
	}
	h.desired <- connected
	receiveSetCall(t, h.setter)
	h.setter.results <- nil
	if got := receiveSetCall(t, h.setter); got.op != "volume" {
		t.Fatalf("volume call = %#v", got)
	}
	h.setter.results <- errors.New("volume failed")
	receiveRetry(t, h.observer)
	timer := <-h.clock.timers
	timer.timer.ticks <- time.Time{}
	receiveSetCall(t, h.setter)
	h.setter.results <- nil
	if got := receiveSetCall(t, h.setter); got.op != "volume" {
		t.Fatalf("retried volume call = %#v", got)
	}

	h.desired <- Desired{ControlsEnabled: true}
	if got := receiveSetCall(t, h.setter); got.op != "" || got.target.Name != "speakers" {
		t.Fatalf("disconnect replacement call = %#v", got)
	}
	h.setter.results <- nil
	receiveSucceeded(t, h.observer)
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
