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

type DefaultSetter interface {
	SetDefault(context.Context, Kind, string) error
}

type Targets struct {
	HeadsetSink    string
	FallbackSink   string
	HeadsetSource  string
	FallbackSource string
}

type Desired struct {
	HeadsetConnected bool
}

type ActionObserver interface {
	AudioActionSucceeded(Desired, int)
	AudioActionRetrying(Desired, int, error)
}

type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

type RetryClock interface {
	NewTimer(time.Duration) Timer
}

type Controller struct {
	setter       DefaultSetter
	targets      Targets
	clock        RetryClock
	observer     ActionObserver
	initialRetry time.Duration
	maximumRetry time.Duration
}

func NewController(
	setter DefaultSetter,
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

func (controller *Controller) Run(ctx context.Context, desiredStates <-chan Desired) error {
	results := make(chan actionResult, 1)
	var revision uint64
	var current Desired
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
		go func() {
			err := controller.apply(actionContext, currentDesired)
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
			attempt = 1
			startAction()
		case result := <-results:
			if result.revision != revision {
				continue
			}
			actionCancel()
			actionCancel = nil
			if result.err == nil {
				controller.observer.AudioActionSucceeded(result.desired, result.attempt)
				continue
			}
			controller.observer.AudioActionRetrying(result.desired, result.attempt, result.err)
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

func (controller *Controller) apply(ctx context.Context, desired Desired) error {
	sink := controller.targets.FallbackSink
	source := controller.targets.FallbackSource
	if desired.HeadsetConnected {
		sink = controller.targets.HeadsetSink
		source = controller.targets.HeadsetSource
	}
	if err := controller.setter.SetDefault(ctx, Sink, sink); err != nil {
		return err
	}
	if source != "" {
		if err := controller.setter.SetDefault(ctx, Source, source); err != nil {
			return err
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
