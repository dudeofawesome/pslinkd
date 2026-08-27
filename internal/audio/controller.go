package audio

import (
	"context"
	"fmt"
	"time"
)

const (
	DefaultInitialRetry = 250 * time.Millisecond
	DefaultMaximumRetry = 30 * time.Second
)

type Setter interface {
	SetDefault(context.Context, Kind, Target) error
	SetVolume(context.Context, Target, uint8) error
	SetMute(context.Context, Target, bool) error
}

type USBIdentity struct {
	Syspath    string
	Serial     string
	HIDSyspath string
	HIDDevnode string
}

type Target struct {
	Name string
	USB  USBIdentity
}

type Targets struct {
	HeadsetSink    string
	FallbackSink   string
	HeadsetSource  string
	FallbackSource string
}

type Desired struct {
	HeadsetConnected bool
	USB              USBIdentity
	ControlsEnabled  bool
	Volume           OptionalVolume
	MicrophoneMuted  OptionalBool
}

type OptionalVolume struct {
	Valid bool
	Value uint8
}

type OptionalBool struct {
	Valid bool
	Value bool
}

type ActionObserver interface {
	AudioActionSucceeded(uint64, Desired, int)
	AudioActionRetrying(uint64, Desired, int, error)
}

type TargetError struct {
	Kind       Kind
	TargetName string
	Err        error
}

func (err *TargetError) Error() string {
	return fmt.Sprintf("route %s target %q: %v", err.Kind, err.TargetName, err.Err)
}

func (err *TargetError) Unwrap() error {
	return err.Err
}

type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

type RetryClock interface {
	NewTimer(time.Duration) Timer
}

type Controller struct {
	setter       Setter
	targets      Targets
	clock        RetryClock
	observer     ActionObserver
	initialRetry time.Duration
	maximumRetry time.Duration
}

func NewController(
	setter Setter,
	targets Targets,
	clock RetryClock,
	observer ActionObserver,
) *Controller {
	return &Controller{
		setter:       setter,
		targets:      targets,
		clock:        clock,
		observer:     observer,
		initialRetry: DefaultInitialRetry,
		maximumRetry: DefaultMaximumRetry,
	}
}

type actionResult struct {
	revision uint64
	desired  Desired
	attempt  int
	err      error
}

type actionPlan struct {
	route  bool
	volume bool
	mute   bool
}

func (controller *Controller) Run(ctx context.Context, desiredStates <-chan Desired) error {
	results := make(chan actionResult, 1)
	var revision uint64
	var current Desired
	var applied *Desired
	var plan actionPlan
	var attempt int
	var actionCancel context.CancelFunc
	var retryTimer Timer
	var retry <-chan time.Time

	stopCurrent := func() {
		if actionCancel != nil {
			actionCancel()
			actionCancel = nil
		}
		if retryTimer != nil {
			retryTimer.Stop()
			retryTimer = nil
			retry = nil
		}
	}
	defer stopCurrent()

	startAction := func() {
		actionContext, cancel := context.WithCancel(ctx)
		actionCancel = cancel
		currentRevision := revision
		currentDesired := current
		currentAttempt := attempt
		currentPlan := plan
		go func() {
			err := controller.apply(actionContext, currentDesired, currentPlan)
			result := actionResult{
				revision: currentRevision,
				desired:  currentDesired,
				attempt:  currentAttempt,
				err:      err,
			}
			select {
			case results <- result:
			case <-actionContext.Done():
			}
		}()
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case desired, ok := <-desiredStates:
			if !ok {
				return fmt.Errorf("desired audio state channel closed")
			}
			stopCurrent()
			revision++
			current = desired
			plan = controller.plan(desired, applied)
			attempt = 1
			startAction()
		case result := <-results:
			if result.revision != revision {
				continue
			}
			actionCancel()
			actionCancel = nil
			if result.err == nil {
				value := result.desired
				applied = &value
				controller.observer.AudioActionSucceeded(
					result.revision,
					result.desired,
					result.attempt,
				)
				continue
			}
			controller.observer.AudioActionRetrying(
				result.revision,
				result.desired,
				result.attempt,
				result.err,
			)
			retryTimer = controller.clock.NewTimer(controller.retryDelay(result.attempt))
			retry = retryTimer.C()
		case <-retry:
			retryTimer = nil
			retry = nil
			attempt++
			startAction()
		}
	}
}

func (controller *Controller) plan(desired Desired, applied *Desired) actionPlan {
	route := applied == nil || desired.HeadsetConnected != applied.HeadsetConnected ||
		(desired.HeadsetConnected && desired.USB != applied.USB)
	volume := desired.ControlsEnabled && desired.HeadsetConnected && desired.Volume.Valid &&
		(applied == nil || !applied.ControlsEnabled || !applied.HeadsetConnected ||
			!applied.Volume.Valid || desired.Volume.Value != applied.Volume.Value ||
			desired.USB != applied.USB)
	mute := controller.targets.FallbackSource != "" && desired.ControlsEnabled &&
		desired.HeadsetConnected && desired.MicrophoneMuted.Valid &&
		(applied == nil || !applied.ControlsEnabled || !applied.HeadsetConnected ||
			!applied.MicrophoneMuted.Valid ||
			desired.MicrophoneMuted.Value != applied.MicrophoneMuted.Value ||
			desired.USB != applied.USB)
	return actionPlan{route: route, volume: volume, mute: mute}
}

func (controller *Controller) apply(ctx context.Context, desired Desired, plan actionPlan) error {
	sink := Target{Name: controller.targets.FallbackSink}
	source := Target{Name: controller.targets.FallbackSource}
	if desired.HeadsetConnected {
		sink = Target{Name: controller.targets.HeadsetSink, USB: desired.USB}
		source = Target{Name: controller.targets.HeadsetSource, USB: desired.USB}
	}
	if plan.route {
		if err := controller.setter.SetDefault(ctx, Sink, sink); err != nil {
			return &TargetError{Kind: Sink, TargetName: sink.Name, Err: err}
		}
		if source.Name != "" || (desired.HeadsetConnected && controller.targets.FallbackSource != "") {
			if err := controller.setter.SetDefault(ctx, Source, source); err != nil {
				return &TargetError{Kind: Source, TargetName: source.Name, Err: err}
			}
		}
	}
	if plan.volume {
		if err := controller.setter.SetVolume(ctx, sink, desired.Volume.Value); err != nil {
			return &TargetError{Kind: Sink, TargetName: sink.Name, Err: err}
		}
	}
	if plan.mute {
		if err := controller.setter.SetMute(ctx, source, desired.MicrophoneMuted.Value); err != nil {
			return &TargetError{Kind: Source, TargetName: source.Name, Err: err}
		}
	}
	return nil
}

func (controller *Controller) retryDelay(failedAttempt int) time.Duration {
	delay := controller.initialRetry
	for attempt := 1; attempt < failedAttempt && delay < controller.maximumRetry; attempt++ {
		if delay > controller.maximumRetry/2 {
			return controller.maximumRetry
		}
		delay *= 2
	}
	if delay > controller.maximumRetry {
		return controller.maximumRetry
	}
	return delay
}

type RealRetryClock struct{}

func (RealRetryClock) NewTimer(delay time.Duration) Timer {
	return &realTimer{Timer: time.NewTimer(delay)}
}

type realTimer struct {
	*time.Timer
}

func (timer *realTimer) C() <-chan time.Time {
	return timer.Timer.C
}
