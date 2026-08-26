package discovery

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeBackend struct {
	candidates []Candidate
	events     []Event
	monitorErr error
	enumerates int
}

func (backend *fakeBackend) Enumerate() ([]Candidate, error) {
	backend.enumerates++
	return append([]Candidate(nil), backend.candidates...), nil
}

func (backend *fakeBackend) Monitor(_ context.Context, emit func(Event) error) error {
	for _, event := range backend.events {
		if err := emit(event); err != nil {
			return err
		}
	}
	return backend.monitorErr
}

type recordingSink struct {
	selections []Candidate
	warnings   [][]Candidate
}

func (sink *recordingSink) SelectionChanged(candidate Candidate) {
	sink.selections = append(sink.selections, candidate)
}

func (sink *recordingSink) MultipleCandidates(candidates []Candidate) {
	sink.warnings = append(sink.warnings, candidates)
}

func TestSelectUsesLexicographicallyFirstSyspath(t *testing.T) {
	selected, sorted := Select([]Candidate{
		{Syspath: "/sys/z", Devnode: "/dev/hidraw9"},
		{Syspath: "/sys/a", Devnode: "/dev/hidraw4"},
	})
	if selected.Syspath != "/sys/a" {
		t.Fatalf("selected %#v", selected)
	}
	if sorted[0].Syspath != "/sys/a" || sorted[1].Syspath != "/sys/z" {
		t.Fatalf("not sorted: %#v", sorted)
	}
}

func TestInitialEnumerationSelectsAndWarnsOnce(t *testing.T) {
	backend := &fakeBackend{
		candidates: []Candidate{
			{Syspath: "/sys/z", Devnode: "/dev/hidraw9"},
			{Syspath: "/sys/a", Devnode: "/dev/hidraw4"},
		},
		events: []Event{{Action: "add", Syspath: "/sys/irrelevant"}},
	}
	sink := &recordingSink{}
	if err := NewWatcher(backend, sink).reconcile(); err != nil {
		t.Fatal(err)
	}
	wantSelections := []Candidate{{Syspath: "/sys/a", Devnode: "/dev/hidraw4"}}
	if !reflect.DeepEqual(sink.selections, wantSelections) {
		t.Fatalf("selections = %#v", sink.selections)
	}
	if len(sink.warnings) != 1 || len(sink.warnings[0]) != 2 {
		t.Fatalf("warnings = %#v", sink.warnings)
	}
}

func TestStartupWithoutAdapterEstablishesAbsence(t *testing.T) {
	backend := &fakeBackend{}
	sink := &recordingSink{}
	if err := NewWatcher(backend, sink).reconcile(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sink.selections, []Candidate{{}}) {
		t.Fatalf("selections = %#v", sink.selections)
	}
}

func TestSelectedRemovalEmitsAbsenceBeforeReplacement(t *testing.T) {
	first := Candidate{Syspath: "/sys/a", Devnode: "/dev/hidraw1"}
	second := Candidate{Syspath: "/sys/b", Devnode: "/dev/hidraw2"}
	backend := &fakeBackend{candidates: []Candidate{first, second}}
	sink := &recordingSink{}
	watcher := NewWatcher(backend, sink)
	if err := watcher.reconcile(); err != nil {
		t.Fatal(err)
	}
	backend.candidates = []Candidate{second}
	if err := watcher.handleEvent(Event{Action: "remove", Syspath: first.Syspath}); err != nil {
		t.Fatal(err)
	}
	want := []Candidate{first, {}, second}
	if !reflect.DeepEqual(sink.selections, want) {
		t.Fatalf("selections = %#v, want %#v", sink.selections, want)
	}
}

func TestIrrelevantAndDuplicateEventsDoNotFlapSelection(t *testing.T) {
	candidate := Candidate{Syspath: "/sys/a", Devnode: "/dev/hidraw1"}
	backend := &fakeBackend{candidates: []Candidate{candidate}}
	sink := &recordingSink{}
	watcher := NewWatcher(backend, sink)
	if err := watcher.reconcile(); err != nil {
		t.Fatal(err)
	}
	for _, event := range []Event{
		{Action: "change", Syspath: candidate.Syspath},
		{Action: "add", Syspath: candidate.Syspath},
		{Action: "remove", Syspath: "/sys/unrelated"},
	} {
		if err := watcher.handleEvent(event); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(sink.selections, []Candidate{candidate}) {
		t.Fatalf("selections = %#v", sink.selections)
	}
}

func TestMonitorFailureIsFatal(t *testing.T) {
	backend := &fakeBackend{monitorErr: errors.New("netlink stopped")}
	err := NewWatcher(backend, &recordingSink{}).Run(context.Background())
	if err == nil || !errors.Is(err, backend.monitorErr) {
		t.Fatalf("error = %v", err)
	}
}

func TestUnexpectedCleanMonitorStopIsFatal(t *testing.T) {
	err := NewWatcher(&fakeBackend{}, &recordingSink{}).Run(context.Background())
	if err == nil {
		t.Fatal("unexpected clean monitor stop was ignored")
	}
}
