package polling

import (
	"context"
	"errors"
	"time"

	"github.com/dudeofawesome/pslinkd/internal/discovery"
	"github.com/dudeofawesome/pslinkd/internal/hid"
	"github.com/dudeofawesome/pslinkd/internal/state"
)

type Reader interface {
	ReadFeature() ([]byte, error)
	Close() error
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

	candidate    discovery.Candidate
	hasSelection bool
	reader       Reader
	hadFailure   bool
}

func New(
	interval time.Duration,
	disconnectFailures int,
	clock Clock,
	open OpenReader,
	observer Observer,
) *Poller {
	return &Poller{
		interval:  interval,
		clock:     clock,
		open:      open,
		observer:  observer,
		debouncer: state.NewDebouncer(disconnectFailures),
	}
}

type readResult struct {
	data []byte
	err  error
}

func (poller *Poller) Run(
	ctx context.Context,
	selections <-chan discovery.Candidate,
) error {
	ticker := poller.clock.NewTicker(poller.interval)
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
		return false
	}
}

func (poller *Poller) selectCandidate(candidate discovery.Candidate) {
	if poller.hasSelection && candidate == poller.candidate {
		return
	}
	if poller.hasSelection && poller.candidate.Devnode != "" {
		poller.closeReader()
		poller.interactions.Reset()
		if connection, changed := poller.debouncer.AdapterAbsent(); changed {
			poller.observer.ConnectionChanged(connection)
		}
	}

	poller.hasSelection = true
	poller.candidate = candidate
	poller.hadFailure = false
	if candidate.Devnode == "" {
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
	if connection, changed := poller.debouncer.Sample(connected); changed {
		if !connection.HeadsetConnected {
			poller.interactions.Reset()
		}
		poller.observer.ConnectionChanged(connection)
		return connection, true
	}
	connection, _ := poller.debouncer.State()
	return connection, false
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
