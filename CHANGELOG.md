# Release documentation

This document tracks the scope and validation requirements of pslinkd release
milestones. The specifications are authoritative; a milestone is complete only
after its automated criteria and applicable hardware release gates pass.

## v1: headset-aware routing

The initial milestone covers the Sony PULSE Elite / CFI-ZWA2 adapter profile
(`054c:0ecc`, HID interface 3), debounced radio-link detection, automatic
WirePlumber audio-node association, fallback routing, Nix packaging, and Home
Manager deployment.

See [`specs/requirements.md`](specs/requirements.md) and
[`specs/testing.md`](specs/testing.md) for its scope and release gates.

## v1.1: button interactions

This milestone adds button-edge events, absolute volume and microphone-state
reporting, and optional WirePlumber control convergence. It preserves the core
routing boundaries: it does not consume the USB interrupt endpoint, force-move
streams, run hooks, or expose an IPC API.

See [`specs/v1.1.md`](specs/v1.1.md) for its acceptance criteria and Olympus
firmware 1.38 release gates.

## v1.1.1: host-only volume approximation

This proposed milestone adds an opt-in mode that restores device volume to its
maximum and converts physical volume-button edges into relative host-volume
steps. It is an approximation, not a hardware DSP bypass.

See [`specs/v1.1.1.md`](specs/v1.1.1.md) for the protocol limitations,
acceptance criteria, and hardware release gates.

## v1.2: battery reporting

This proposed milestone adds change-only structured battery reporting from HID
feature report `0x82`.

See [`specs/v1.2.md`](specs/v1.2.md) for its acceptance criteria and hardware
release gates.
