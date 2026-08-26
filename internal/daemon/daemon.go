package daemon

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/dudeofawesome/pslinkd/internal/audio"
	"github.com/dudeofawesome/pslinkd/internal/config"
	"github.com/dudeofawesome/pslinkd/internal/discovery"
	"github.com/dudeofawesome/pslinkd/internal/hid"
	"github.com/dudeofawesome/pslinkd/internal/logging"
	"github.com/dudeofawesome/pslinkd/internal/polling"
	"github.com/dudeofawesome/pslinkd/internal/state"
)

const (
	commandTimeout      = 5 * time.Second
	failureLogInterval  = 30 * time.Second
	selectionBufferSize = 1
	desiredBufferSize   = 1
)

type Dependencies struct {
	DiscoveryBackend discovery.Backend
	OpenReader       polling.OpenReader
	PollClock        polling.Clock
	AudioSetter      audio.DefaultSetter
	RetryClock       audio.RetryClock
}

func ProductionDependencies() Dependencies {
	return Dependencies{
		DiscoveryBackend: discovery.NewBackend(),
		OpenReader: func(path string) (polling.Reader, error) {
			return hid.Open(path)
		},
		PollClock: polling.RealClock{},
		AudioSetter: audio.NewWPCTL(
			audio.ExecRunner{Path: "wpctl"},
			commandTimeout,
		),
		RetryClock: audio.RealRetryClock{},
	}
}

func Run(
	ctx context.Context,
	cfg config.Config,
	logger *logging.Logger,
	dependencies Dependencies,
) error {
	logger.Event(logging.Info, "daemon_start", "pslinkd started", nil)
	defer logger.Event(logging.Info, "daemon_stop", "pslinkd stopped", nil)

	workerContext, cancel := context.WithCancel(ctx)
	defer cancel()

	selections := make(chan discovery.Candidate, selectionBufferSize)
	desiredStates := make(chan audio.Desired, desiredBufferSize)
	observer := &observer{
		logger:        logger,
		selections:    selections,
		desiredStates: desiredStates,
		targets: audio.Targets{
			HeadsetSink:    cfg.Audio.HeadsetSink,
			FallbackSink:   cfg.Audio.FallbackSink,
			HeadsetSource:  cfg.Audio.HeadsetSource,
			FallbackSource: cfg.Audio.FallbackSource,
		},
	}

	watcher := discovery.NewWatcher(dependencies.DiscoveryBackend, observer)
	poller := polling.New(
		cfg.Polling.Interval.Duration,
		cfg.Polling.DisconnectFailures,
		dependencies.PollClock,
		dependencies.OpenReader,
		observer,
	)
	controller := audio.NewController(
		dependencies.AudioSetter,
		observer.targets,
		dependencies.RetryClock,
		observer,
	)

	type workerResult struct {
		name string
		err  error
	}
	results := make(chan workerResult, 3)
	start := func(name string, run func() error) {
		go func() { results <- workerResult{name: name, err: run()} }()
	}
	start("discovery", func() error { return watcher.Run(workerContext) })
	start("polling", func() error { return poller.Run(workerContext, selections) })
	start("audio", func() error { return controller.Run(workerContext, desiredStates) })

	var fatal error
	for range 3 {
		result := <-results
		if result.err == nil && workerContext.Err() == nil {
			result.err = errors.New("worker stopped unexpectedly")
		}
		if result.err == nil || fatal != nil {
			continue
		}
		fatal = fmt.Errorf("%s worker: %w", result.name, result.err)
		event := "daemon_fatal"
		if result.name == "discovery" {
			event = "discovery_fatal"
		}
		logger.Event(logging.Error, event, result.name+" worker failed", logging.Fields{
			"error": fatal.Error(),
		})
		cancel()
	}
	if ctx.Err() != nil {
		return nil
	}
	return fatal
}

type observer struct {
	logger        *logging.Logger
	selections    chan discovery.Candidate
	desiredStates chan audio.Desired
	targets       audio.Targets
	selected      discovery.Candidate
	hidEpisode    atomic.Uint64
}

func (observer *observer) SelectionChanged(candidate discovery.Candidate) {
	if observer.selected.Devnode != "" && observer.selected != candidate {
		observer.logger.Event(logging.Info, "adapter_removed", "PlayStation Link adapter removed", logging.Fields{
			"device_path":     observer.selected.Devnode,
			"adapter_present": false,
		})
	}
	if candidate.Devnode != "" && observer.selected != candidate {
		observer.logger.Event(logging.Info, "adapter_added", "PlayStation Link adapter added", logging.Fields{
			"device_path":     candidate.Devnode,
			"adapter_present": true,
		})
	}
	if observer.selected == (discovery.Candidate{}) && candidate == (discovery.Candidate{}) {
		observer.logger.Event(logging.Info, "adapter_removed", "PlayStation Link adapter is absent", logging.Fields{
			"adapter_present": false,
		})
	}
	observer.selected = candidate
	observer.hidEpisode.Add(1)
	sendLatest(observer.selections, candidate)
}

func (observer *observer) MultipleCandidates(candidates []discovery.Candidate) {
	paths := make([]string, len(candidates))
	for index, candidate := range candidates {
		paths[index] = candidate.Syspath
	}
	observer.logger.Event(logging.Warn, "multiple_adapters", "multiple supported adapters found", logging.Fields{
		"candidates": paths,
	})
}

func (observer *observer) ConnectionChanged(connection state.Connection) {
	event := "headset_disconnected"
	message := "headset radio link disconnected"
	if connection.HeadsetConnected {
		event = "headset_connected"
		message = "headset radio link connected"
	}
	observer.logger.Event(logging.Info, event, message, logging.Fields{
		"adapter_present":   connection.AdapterPresent,
		"headset_connected": connection.HeadsetConnected,
	})
	sendLatest(observer.desiredStates, audio.Desired{
		HeadsetConnected: connection.HeadsetConnected,
	})
}

func (observer *observer) HIDFailure(path string, err error) {
	key := fmt.Sprintf("hid:%d:%s:%s", observer.hidEpisode.Load(), reflect.TypeOf(err), err)
	observer.logger.RateLimited(
		key,
		failureLogInterval,
		logging.Warn,
		"hid_failure",
		"HID feature report failed",
		logging.Fields{
			"device_path": path,
			"error":       err.Error(),
			"error_type":  fmt.Sprintf("%T", err),
		},
	)
}

func (observer *observer) HIDRecovered(path string) {
	observer.hidEpisode.Add(1)
	observer.logger.Event(logging.Info, "hid_recovered", "HID feature reports recovered", logging.Fields{
		"device_path": path,
	})
}

func (observer *observer) AudioActionSucceeded(
	revision uint64,
	desired audio.Desired,
	attempt int,
) {
	fields := logging.Fields{
		"attempt":     attempt,
		"revision":    revision,
		"target_name": observer.sinkTarget(desired),
	}
	if source := observer.sourceTarget(desired); source != "" {
		fields["source_target_name"] = source
	}
	observer.logger.Event(logging.Info, "audio_action_succeeded", "audio defaults updated", fields)
}

func (observer *observer) AudioActionRetrying(
	revision uint64,
	desired audio.Desired,
	attempt int,
	err error,
) {
	targetName := observer.sinkTarget(desired)
	var targetError *audio.TargetError
	if errors.As(err, &targetError) {
		targetName = targetError.TargetName
	}
	key := fmt.Sprintf("audio:%d:%s:%s", revision, targetName, rootError(err))
	observer.logger.RateLimited(
		key,
		failureLogInterval,
		logging.Warn,
		"audio_action_retrying",
		"audio action failed and will be retried",
		logging.Fields{
			"attempt":     attempt,
			"revision":    revision,
			"target_name": targetName,
			"error":       err.Error(),
		},
	)
}

func (observer *observer) sinkTarget(desired audio.Desired) string {
	if desired.HeadsetConnected {
		return observer.targets.HeadsetSink
	}
	return observer.targets.FallbackSink
}

func (observer *observer) sourceTarget(desired audio.Desired) string {
	if desired.HeadsetConnected {
		return observer.targets.HeadsetSource
	}
	return observer.targets.FallbackSource
}

func rootError(err error) string {
	for errors.Unwrap(err) != nil {
		err = errors.Unwrap(err)
	}
	return err.Error()
}

func sendLatest[T any](channel chan T, value T) {
	select {
	case channel <- value:
		return
	default:
	}
	select {
	case <-channel:
	default:
	}
	channel <- value
}
