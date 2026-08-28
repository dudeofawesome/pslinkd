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
	HostVolumeDelta       int
	InferredVolumeSteps   int
}

func (update InteractionUpdate) Changed() bool {
	return update.VolumeChanged || update.MicrophoneChanged ||
		update.VolumeUpPressed || update.VolumeDownPressed || update.MicrophoneMutePressed ||
		update.HostVolumeDelta != 0
}

type InteractionTracker struct {
	established          bool
	controls             Controls
	volumeUp             bool
	volumeDown           bool
	mute                 bool
	pendingUpCredits     int
	pendingDownCredits   int
	pendingCreditSamples int
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

		if tracker.pendingUpCredits > 0 || tracker.pendingDownCredits > 0 {
			tracker.pendingCreditSamples++
			if tracker.pendingCreditSamples > 4 {
				tracker.pendingUpCredits = 0
				tracker.pendingDownCredits = 0
				tracker.pendingCreditSamples = 0
			}
		}

		rawDelta := 0
		if next.Volume.Valid && tracker.controls.Volume.Valid {
			previous := int(tracker.controls.Volume.Value)
			current := int(next.Volume.Value)
			target := int(hid.DeviceVolumeTarget)
			switch {
			case current == target && previous != target:
				// A return to the fixed baseline is device convergence, not a
				// physical press in the opposite direction.
				tracker.pendingUpCredits = 0
				tracker.pendingDownCredits = 0
				tracker.pendingCreditSamples = 0
			case previous == target:
				rawDelta = current - target
			case (previous-target)*(current-target) < 0:
				// A baseline restoration and a new press happened between
				// samples. Count only the new excursion from the target.
				tracker.pendingUpCredits = 0
				tracker.pendingDownCredits = 0
				tracker.pendingCreditSamples = 0
				rawDelta = current - target
			default:
				rawDelta = current - previous
			}
		}

		remaining := rawDelta
		if remaining > 0 {
			consumed := min(remaining, tracker.pendingUpCredits)
			remaining -= consumed
			tracker.pendingUpCredits -= consumed
		} else if remaining < 0 {
			consumed := min(-remaining, tracker.pendingDownCredits)
			remaining += consumed
			tracker.pendingDownCredits -= consumed
		}
		if tracker.pendingUpCredits == 0 && tracker.pendingDownCredits == 0 {
			tracker.pendingCreditSamples = 0
		}

		upSteps := 0
		if update.VolumeUpPressed {
			upSteps = 1
			if remaining > 0 {
				remaining--
			} else {
				tracker.pendingUpCredits++
				tracker.pendingCreditSamples = 0
			}
		}
		downSteps := 0
		if update.VolumeDownPressed {
			downSteps = 1
			if remaining < 0 {
				remaining++
			} else {
				tracker.pendingDownCredits++
				tracker.pendingCreditSamples = 0
			}
		}
		update.HostVolumeDelta = upSteps - downSteps + remaining
		update.InferredVolumeSteps = remaining
	}

	tracker.established = true
	tracker.controls = next
	tracker.volumeUp = report.VolumeUpPressed
	tracker.volumeDown = report.VolumeDownPressed
	tracker.mute = report.MicrophoneMutePressed
	return update
}
