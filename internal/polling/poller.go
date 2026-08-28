package polling

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/dudeofawesome/pslinkd/internal/discovery"
	"github.com/dudeofawesome/pslinkd/internal/hid"
	"github.com/dudeofawesome/pslinkd/internal/state"
)

type Reader interface {
	ReadFeature() ([]byte, error)
	Close() error
}

type Writer interface {
	WriteFeature([]byte) error
}

type OpenReader func(string) (Reader, error)

type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type Clock interface {
	NewTicker(time.Duration) Ticker
}

type Observer interface {
	ConnectionChanged(state.Connection)
	InteractionChanged(state.InteractionUpdate)
	InvalidVolume(string, uint8)
	HIDFailure(string, error)
	HIDRecovered(string)
}

type Poller struct {
	interval     time.Duration
	clock        Clock
	open         OpenReader
	observer     Observer
	debouncer    *state.Debouncer
	interactions state.InteractionTracker

	candidate       discovery.Candidate
	hasSelection    bool
	reader          Reader
	hadFailure      bool
	hostOnlyVolume  atomic.Bool
	restoreActive   bool
	writeInFlight   bool
	waitingReadback bool
	writeAttempt    int
	retryPolls      int
	failureElapsed  time.Duration
	writeGeneration uint64
	writeResults    chan writeResult
}

func New(
	interval time.Duration,
	disconnectFailures int,
	clock Clock,
	open OpenReader,
	observer Observer,
) *Poller {
	return &Poller{
		interval:     interval,
		clock:        clock,
		open:         open,
		observer:     observer,
		debouncer:    state.NewDebouncer(disconnectFailures),
		writeResults: make(chan writeResult, 1),
	}
}

func (poller *Poller) SetHostOnlyVolume(enabled bool) {
	poller.hostOnlyVolume.Store(enabled)
}

type readResult struct {
	data []byte
	err  error
}

type writeResult struct {
	generation uint64
	err        error
}

func (poller *Poller) Run(
	ctx context.Context,
	selections <-chan discovery.Candidate,
) error {
	ticker := poller.clock.NewTicker(poller.pollInterval())
	defer ticker.Stop()
	defer poller.closeReader()

	for {
		select {
		case <-ctx.Done():
			return nil
		case candidate, ok := <-selections:
			if !ok {
				return errors.New("discovery selection channel closed")
			}
			poller.selectCandidate(candidate)
		case result := <-poller.writeResults:
			poller.finishDeviceVolumeWrite(result)
		case <-ticker.C():
			if poller.candidate.Devnode == "" {
				continue
			}
			if poller.reader == nil {
				reader, err := poller.open(poller.candidate.Devnode)
				if err != nil {
					poller.failedSample(err)
					continue
				}
				poller.reader = reader
			}
			results := make(chan readResult, 1)
			reader := poller.reader
			go func() {
				data, err := reader.ReadFeature()
				results <- readResult{data: data, err: err}
			}()
			if done := poller.waitForRead(ctx, selections, results); done {
				return nil
			}
		}
	}
}

func (poller *Poller) waitForRead(
	ctx context.Context,
	selections <-chan discovery.Candidate,
	results <-chan readResult,
) bool {
	for {
		select {
		case <-ctx.Done():
			poller.closeReader()
			return true
		case candidate, ok := <-selections:
			if !ok {
				poller.closeReader()
				return true
			}
			poller.selectCandidate(candidate)
			return false
		case result := <-poller.writeResults:
			poller.finishDeviceVolumeWrite(result)
			continue
		case result := <-results:
			if result.err != nil {
				poller.failedSample(result.err)
				return false
			}
			report, err := hid.DecodeReport(result.data)
			if err != nil {
				poller.failedSample(err)
				return false
			}
			poller.recovered()
			connection, _ := poller.sample(report.Connected)
			if !connection.HeadsetConnected {
				return false
			}
			if report.InvalidVolume != nil {
				poller.observer.InvalidVolume(poller.candidate.Devnode, *report.InvalidVolume)
			}
			if update := poller.interactions.Sample(report); update.Changed() {
				poller.observer.InteractionChanged(update)
			}
			poller.convergeDeviceVolume(report.Volume)
			return false
		}
	}
}

func (poller *Poller) selectCandidate(candidate discovery.Candidate) {
	if poller.hasSelection && candidate == poller.candidate {
		return
	}
	if poller.hasSelection && poller.candidate.Devnode != "" {
		poller.resetDeviceVolumeRestore()
		poller.closeReader()
		poller.interactions.Reset()
		if connection, changed := poller.debouncer.AdapterAbsent(); changed {
			poller.observer.ConnectionChanged(connection)
		}
	}

	poller.hasSelection = true
	poller.candidate = candidate
	poller.hadFailure = false
	poller.failureElapsed = 0
	if candidate.Devnode == "" {
		poller.resetDeviceVolumeRestore()
		poller.interactions.Reset()
		if connection, changed := poller.debouncer.AdapterAbsent(); changed {
			poller.observer.ConnectionChanged(connection)
		}
		return
	}
	poller.debouncer.AdapterAdded()
}

func (poller *Poller) failedSample(err error) {
	if !hid.IsExpectedReadError(err) {
		poller.hadFailure = true
		poller.observer.HIDFailure(poller.candidate.Devnode, err)
	}
	poller.sample(false)
}

func (poller *Poller) recovered() {
	if !poller.hadFailure {
		return
	}
	poller.hadFailure = false
	poller.observer.HIDRecovered(poller.candidate.Devnode)
}

func (poller *Poller) sample(connected bool) (state.Connection, bool) {
	if connected {
		poller.failureElapsed = 0
	} else {
		actualInterval := poller.pollInterval()
		debounceInterval := poller.interval
		if debounceInterval <= 0 {
			debounceInterval = actualInterval
		}
		poller.failureElapsed += actualInterval
		if poller.failureElapsed < debounceInterval {
			connection, _ := poller.debouncer.State()
			return connection, false
		}
		poller.failureElapsed -= debounceInterval
	}
	if connection, changed := poller.debouncer.Sample(connected); changed {
		if !connection.HeadsetConnected {
			poller.interactions.Reset()
			poller.resetDeviceVolumeRestore()
			poller.closeReader()
		}
		poller.observer.ConnectionChanged(connection)
		return connection, true
	}
	connection, _ := poller.debouncer.State()
	return connection, false
}

func (poller *Poller) pollInterval() time.Duration {
	if poller.hostOnlyVolume.Load() && poller.interval > 50*time.Millisecond {
		return 50 * time.Millisecond
	}
	return poller.interval
}

func (poller *Poller) convergeDeviceVolume(volume *uint8) {
	if !poller.hostOnlyVolume.Load() || volume == nil {
		return
	}
	if *volume == hid.DeviceVolumeTarget {
		if poller.restoreActive {
			poller.restoreActive = false
			poller.waitingReadback = false
			if observer, ok := poller.observer.(interface{ DeviceVolumeRestored(string) }); ok {
				observer.DeviceVolumeRestored(poller.candidate.Devnode)
			}
		}
		return
	}
	poller.restoreActive = true
	if poller.writeInFlight {
		return
	}
	if poller.retryPolls > 0 {
		poller.retryPolls--
		return
	}
	// A successful write is not convergence. Wait for one authoritative B0
	// readback before retrying an unconfirmed restore.
	if poller.waitingReadback {
		poller.waitingReadback = false
	}
	writer, ok := poller.reader.(Writer)
	if !ok {
		poller.reportDeviceVolumeWriteFailure(errors.New("HID reader does not support feature writes"))
		poller.scheduleWriteRetry()
		return
	}
	poller.writeInFlight = true
	poller.writeAttempt++
	payload := hid.TargetDeviceVolumePayload()
	generation := poller.writeGeneration
	go func() {
		poller.writeResults <- writeResult{
			generation: generation,
			err:        writer.WriteFeature(payload),
		}
	}()
}

func (poller *Poller) finishDeviceVolumeWrite(result writeResult) {
	if result.generation != poller.writeGeneration {
		return
	}
	poller.writeInFlight = false
	if result.err != nil {
		poller.waitingReadback = false
		poller.scheduleWriteRetry()
		if !hid.IsExpectedReadError(result.err) {
			poller.reportDeviceVolumeWriteFailure(result.err)
		}
		return
	}
	poller.writeAttempt = 0
	poller.retryPolls = 0
	poller.waitingReadback = true
	if observer, ok := poller.observer.(interface{ DeviceVolumeWriteRecovered(string) }); ok {
		observer.DeviceVolumeWriteRecovered(poller.candidate.Devnode)
	}
}

func (poller *Poller) reportDeviceVolumeWriteFailure(err error) {
	if observer, ok := poller.observer.(interface{ DeviceVolumeWriteFailure(string, error) }); ok {
		observer.DeviceVolumeWriteFailure(poller.candidate.Devnode, err)
	}
}

func (poller *Poller) resetDeviceVolumeRestore() {
	poller.writeGeneration++
	poller.restoreActive = false
	poller.waitingReadback = false
	poller.writeInFlight = false
	poller.writeAttempt = 0
	poller.retryPolls = 0
}

func (poller *Poller) scheduleWriteRetry() {
	delay := 250 * time.Millisecond
	for attempt := 1; attempt < poller.writeAttempt && delay < 30*time.Second; attempt++ {
		delay *= 2
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
	}
	interval := poller.pollInterval()
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	poller.retryPolls = int((delay + interval - 1) / interval)
}

func (poller *Poller) closeReader() {
	if poller.reader == nil {
		return
	}
	_ = poller.reader.Close()
	poller.reader = nil
}

type RealClock struct{}

func (RealClock) NewTicker(interval time.Duration) Ticker {
	return realTicker{Ticker: time.NewTicker(interval)}
}

type realTicker struct {
	*time.Ticker
}

func (ticker realTicker) C() <-chan time.Time {
	return ticker.Ticker.C
}
