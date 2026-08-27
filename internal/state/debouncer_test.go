package state

import (
	"testing"

	"github.com/dudeofawesome/pslinkd/internal/hid"
)

func TestImmediateConnectionAndThresholdDisconnect(t *testing.T) {
	debouncer := NewDebouncer(3)
	debouncer.AdapterAdded()
	state, changed := debouncer.Sample(true)
	if !changed || !state.HeadsetConnected {
		t.Fatalf("connection = %#v, changed = %v", state, changed)
	}
	for sample := 1; sample <= 2; sample++ {
		state, changed = debouncer.Sample(false)
		if changed || !state.HeadsetConnected {
			t.Fatalf("failure %d = %#v, changed = %v", sample, state, changed)
		}
	}
	state, changed = debouncer.Sample(false)
	if !changed || state.HeadsetConnected {
		t.Fatalf("third failure = %#v, changed = %v", state, changed)
	}
	if _, changed = debouncer.Sample(false); changed {
		t.Fatal("steady disconnection emitted another transition")
	}
}

func TestSuccessfulSampleResetsFailures(t *testing.T) {
	debouncer := NewDebouncer(3)
	debouncer.AdapterAdded()
	debouncer.Sample(true)
	debouncer.Sample(false)
	debouncer.Sample(false)
	if _, changed := debouncer.Sample(true); changed {
		t.Fatal("steady connected sample emitted a transition")
	}
	if _, changed := debouncer.Sample(false); changed {
		t.Fatal("failure after reset disconnected")
	}
}

func TestAdapterAbsenceEstablishesFallbackImmediately(t *testing.T) {
	debouncer := NewDebouncer(3)
	state, changed := debouncer.AdapterAbsent()
	if !changed || state.AdapterPresent || state.HeadsetConnected {
		t.Fatalf("absence = %#v, changed = %v", state, changed)
	}

	debouncer.AdapterAdded()
	debouncer.Sample(true)
	state, changed = debouncer.AdapterAbsent()
	if !changed || state.AdapterPresent || state.HeadsetConnected {
		t.Fatalf("removal = %#v, changed = %v", state, changed)
	}
}

func TestPoweredOffAdapterEstablishesFallbackAtThreshold(t *testing.T) {
	debouncer := NewDebouncer(3)
	debouncer.AdapterAdded()
	for sample := 1; sample < 3; sample++ {
		if _, changed := debouncer.Sample(false); changed {
			t.Fatalf("sample %d established state early", sample)
		}
	}
	state, changed := debouncer.Sample(false)
	if !changed || !state.AdapterPresent || state.HeadsetConnected {
		t.Fatalf("threshold state = %#v, changed = %v", state, changed)
	}
}

func volume(value uint8) *uint8 { return &value }

func TestInteractionTrackerBaselinesButtonsAndEmitsInitialState(t *testing.T) {
	tracker := &InteractionTracker{}
	update := tracker.Sample(hid.Report{
		VolumeUpPressed:       true,
		VolumeDownPressed:     true,
		MicrophoneMutePressed: true,
		MicrophoneMuted:       true,
		Volume:                volume(7),
	})
	if update.VolumeUpPressed || update.VolumeDownPressed || update.MicrophoneMutePressed {
		t.Fatalf("baseline synthesized button event: %#v", update)
	}
	if !update.VolumeChanged || !update.MicrophoneChanged ||
		update.Volume != (OptionalVolume{Valid: true, Value: 7}) ||
		update.MicrophoneMuted != (OptionalBool{Valid: true, Value: true}) {
		t.Fatalf("initial normalized state = %#v", update)
	}
}

func TestInteractionTrackerRisingEdgesAreIndependentAndDoNotRepeat(t *testing.T) {
	tracker := &InteractionTracker{}
	base := hid.Report{Volume: volume(4)}
	tracker.Sample(base)

	held := base
	held.VolumeUpPressed = true
	held.VolumeDownPressed = true
	held.MicrophoneMutePressed = true
	first := tracker.Sample(held)
	if !first.VolumeUpPressed || !first.VolumeDownPressed || !first.MicrophoneMutePressed {
		t.Fatalf("simultaneous rising edges = %#v", first)
	}
	second := tracker.Sample(held)
	if second.VolumeUpPressed || second.VolumeDownPressed || second.MicrophoneMutePressed {
		t.Fatalf("held buttons repeated = %#v", second)
	}
	tracker.Sample(base)
	if again := tracker.Sample(held); !again.VolumeUpPressed ||
		!again.VolumeDownPressed || !again.MicrophoneMutePressed {
		t.Fatalf("second rising edges = %#v", again)
	}
}

func TestInteractionTrackerStateChangesInvalidVolumeAndReset(t *testing.T) {
	tracker := &InteractionTracker{}
	tracker.Sample(hid.Report{Volume: volume(3)})
	changed := tracker.Sample(hid.Report{Volume: volume(9), MicrophoneMuted: true})
	if !changed.VolumeChanged || !changed.MicrophoneChanged ||
		changed.Volume.Value != 9 || !changed.MicrophoneMuted.Value {
		t.Fatalf("changed state = %#v", changed)
	}

	invalid := uint8(16)
	omitted := tracker.Sample(hid.Report{InvalidVolume: &invalid, MicrophoneMuted: true})
	if !omitted.VolumeChanged || omitted.Volume.Valid {
		t.Fatalf("invalid volume was not omitted: %#v", omitted)
	}

	tracker.Reset()
	baseline := tracker.Sample(hid.Report{VolumeUpPressed: true, Volume: volume(9)})
	if baseline.VolumeUpPressed || !baseline.VolumeChanged || !baseline.MicrophoneChanged {
		t.Fatalf("reset baseline = %#v", baseline)
	}
}
