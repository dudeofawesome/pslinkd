package discovery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Candidate struct {
	Syspath string
	Devnode string
}

type Event struct {
	Action  string
	Syspath string
}

type Backend interface {
	Enumerate() ([]Candidate, error)
	Monitor(context.Context, func(Event) error) error
}

type Sink interface {
	SelectionChanged(Candidate)
	MultipleCandidates([]Candidate)
}

type Watcher struct {
	backend Backend
	sink    Sink

	selected       Candidate
	established    bool
	lastWarningKey string
}

func NewWatcher(backend Backend, sink Sink) *Watcher {
	return &Watcher{backend: backend, sink: sink}
}

func Select(candidates []Candidate) (Candidate, []Candidate) {
	sorted := append([]Candidate(nil), candidates...)
	sort.Slice(sorted, func(left, right int) bool {
		return sorted[left].Syspath < sorted[right].Syspath
	})
	if len(sorted) == 0 {
		return Candidate{}, sorted
	}
	return sorted[0], sorted
}

func (watcher *Watcher) Run(ctx context.Context) error {
	if err := watcher.reconcile(); err != nil {
		return fmt.Errorf("initial hidraw enumeration: %w", err)
	}
	err := watcher.backend.Monitor(ctx, watcher.handleEvent)
	if ctx.Err() != nil {
		return nil
	}
	if err != nil {
		return fmt.Errorf("udev monitor: %w", err)
	}
	return errors.New("udev monitor stopped unexpectedly")
}

func (watcher *Watcher) handleEvent(event Event) error {
	if event.Action != "add" && event.Action != "remove" {
		return nil
	}
	if event.Action == "remove" &&
		watcher.established &&
		event.Syspath == watcher.selected.Syspath {
		watcher.selected = Candidate{}
		watcher.sink.SelectionChanged(Candidate{})
	}
	if err := watcher.reconcile(); err != nil {
		return fmt.Errorf("re-enumerate after udev %s event: %w", event.Action, err)
	}
	return nil
}

func (watcher *Watcher) reconcile() error {
	candidates, err := watcher.backend.Enumerate()
	if err != nil {
		return err
	}
	selected, sorted := Select(candidates)
	watcher.warnIfMultiple(sorted)

	if !watcher.established || selected != watcher.selected {
		watcher.selected = selected
		watcher.established = true
		watcher.sink.SelectionChanged(selected)
	}
	return nil
}

func (watcher *Watcher) warnIfMultiple(candidates []Candidate) {
	if len(candidates) < 2 {
		watcher.lastWarningKey = ""
		return
	}
	syspaths := make([]string, len(candidates))
	for index, candidate := range candidates {
		syspaths[index] = candidate.Syspath
	}
	key := strings.Join(syspaths, "\x00")
	if key == watcher.lastWarningKey {
		return
	}
	watcher.lastWarningKey = key
	watcher.sink.MultipleCandidates(append([]Candidate(nil), candidates...))
}
