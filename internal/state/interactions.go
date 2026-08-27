package state

import "github.com/dudeofawesome/pslinkd/internal/hid"

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
