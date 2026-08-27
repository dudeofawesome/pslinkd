package state

import "github.com/dudeofawesome/pslinkd/internal/hid"

type Connection struct {
	AdapterPresent   bool
	HeadsetConnected bool
}

type Debouncer struct {
	threshold   int
	state       Connection
	established bool
	failures    int
}

func NewDebouncer(disconnectFailures int) *Debouncer {
	if disconnectFailures < 1 {
		panic("disconnect failure threshold must be positive")
	}
	return &Debouncer{threshold: disconnectFailures}
}

func (debouncer *Debouncer) AdapterAdded() {
	debouncer.state.AdapterPresent = true
	debouncer.failures = 0
}

func (debouncer *Debouncer) AdapterAbsent() (Connection, bool) {
	changed := !debouncer.established ||
		debouncer.state.AdapterPresent ||
		debouncer.state.HeadsetConnected
	debouncer.state = Connection{}
	debouncer.established = true
	debouncer.failures = 0
	return debouncer.state, changed
}

func (debouncer *Debouncer) Sample(connected bool) (Connection, bool) {
	if !debouncer.state.AdapterPresent {
		return debouncer.state, false
	}
	if connected {
		debouncer.failures = 0
		changed := !debouncer.established || !debouncer.state.HeadsetConnected
		debouncer.established = true
		debouncer.state.HeadsetConnected = true
		return debouncer.state, changed
	}

	debouncer.failures++
	if debouncer.failures < debouncer.threshold {
		return debouncer.state, false
	}
	changed := !debouncer.established || debouncer.state.HeadsetConnected
	debouncer.established = true
	debouncer.state.HeadsetConnected = false
	return debouncer.state, changed
}

func (debouncer *Debouncer) State() (Connection, bool) {
	return debouncer.state, debouncer.established
}

type OptionalVolume struct {
	Valid bool
	Value uint8
}

type OptionalBool struct {
	Valid bool
	Value bool
}

type Controls struct {
	Volume          OptionalVolume
	MicrophoneMuted OptionalBool
}

type InteractionUpdate struct {
	Controls
	VolumeChanged         bool
	MicrophoneChanged     bool
	VolumeUpPressed       bool
	VolumeDownPressed     bool
	MicrophoneMutePressed bool
}

func (update InteractionUpdate) Changed() bool {
	return update.VolumeChanged || update.MicrophoneChanged ||
		update.VolumeUpPressed || update.VolumeDownPressed || update.MicrophoneMutePressed
}

type InteractionTracker struct {
	established bool
	controls    Controls
	volumeUp    bool
	volumeDown  bool
	mute        bool
}

func (tracker *InteractionTracker) Reset() {
	*tracker = InteractionTracker{}
}

func (tracker *InteractionTracker) Sample(report hid.Report) InteractionUpdate {
	next := Controls{
		MicrophoneMuted: OptionalBool{Valid: true, Value: report.MicrophoneMuted},
	}
	if report.Volume != nil {
		next.Volume = OptionalVolume{Valid: true, Value: *report.Volume}
	}

	update := InteractionUpdate{Controls: next}
	if !tracker.established {
		update.VolumeChanged = next.Volume.Valid
		update.MicrophoneChanged = true
	} else {
		update.VolumeChanged = next.Volume != tracker.controls.Volume
		update.MicrophoneChanged = next.MicrophoneMuted != tracker.controls.MicrophoneMuted
		update.VolumeUpPressed = report.VolumeUpPressed && !tracker.volumeUp
		update.VolumeDownPressed = report.VolumeDownPressed && !tracker.volumeDown
		update.MicrophoneMutePressed = report.MicrophoneMutePressed && !tracker.mute
	}

	tracker.established = true
	tracker.controls = next
	tracker.volumeUp = report.VolumeUpPressed
	tracker.volumeDown = report.VolumeDownPressed
	tracker.mute = report.MicrophoneMutePressed
	return update
}
