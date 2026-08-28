# pslinkd implementation tasks

Tasks are ordered by dependency. A task is complete only when its relevant
acceptance criteria in `specs/testing.md` pass. Behavior changes require a spec
change first.

## 1. Project and build foundation

- [x] Initialize the Go module and choose the minimum Go version supplied by the
  pinned Nix development environment.
- [x] Add only the required dependencies: strict YAML decoding, libudev Go/cgo
  bindings, and Linux syscall support.
- [x] Populate `devenv.nix` with Go, cgo/pkg-config, libudev, WirePlumber, and
  repository formatting/test tools.
- [x] Add repository-wide Go test, formatting, and vet commands documented in
  `CONTRIBUTING.md`.

## 2. Configuration and CLI

- [x] Implement `pslinkd run [--config PATH]` and XDG config-path resolution.
- [x] Implement the strict YAML schema and all defaults/bounds from
  `specs/configuration.md`.
- [x] Validate the full config before constructing discovery or audio adapters.
- [x] Add configuration and CLI acceptance tests.

## 3. HID device profile and connection decoder

- [x] Model the fixed `054c:0ecc` interface-3 device profile without generic
  VID/PID configuration.
- [x] Implement portable `HIDIOCGFEATURE(64)` request construction and a direct
  hidraw feature reader that never consumes the interrupt stream.
- [x] Decode the connection bit into normalized v1 state and ignore deferred
  button, volume, and microphone fields.
- [x] Add fixture-driven ioctl and connection-decoder tests.

## 4. libudev discovery and device lifecycle

- [x] Enumerate matching hidraw devices at startup using libudev.
- [x] Subscribe to processed libudev add/remove events and filter them to the
  fixed device profile.
- [x] Implement deterministic first-syspath selection and multiple-candidate
  warnings.
- [x] Recover from removal/replug and monitor failure without retaining stale
  file descriptors or state.
- [x] Add discovery tests using an injected fake enumerator/monitor.

## 5. Polling and normalized state machine

- [x] Build a cancellable polling loop independent of audio actions.
- [x] Implement immediate connect, configurable consecutive-failure disconnect,
  and immediate physical-removal behavior.
- [x] Implement initial/fallback state and normalized connection transitions.
- [x] Add fake-clock state-machine tests for every acceptance sequence.

## 6. WirePlumber adapter and policy

- [x] Implement a bounded-time command runner for packaged `wpctl`.
- [x] Parse machine-readable sink/source lists and resolve exact names to current
  IDs at action time.
- [x] Implement transition-only default sink and optional source routing.
- [x] Implement desired-action revisions with bounded exponential retry and
  cancellation when desired state changes.
- [x] Prove through tests that pinned streams are never enumerated/moved and
  later user default changes are not periodically overwritten.

## 6a. Automatic headset audio-target discovery

- [x] Make headset sink configuration optional while retaining exact-name
  overrides; make fallback source alone enable automatic headset-source
  routing.
- [x] Retain the selected HID candidate's parent USB-device identity and carry
  it through desired connected audio actions.
- [x] Inspect current WirePlumber audio devices and associate exactly the same
  physical USB adapter without guessing from VID/PID alone.
- [x] Resolve matching sink/source nodes by current `device.id`, greatest
  `priority.session`, and lexicographic `node.name`, warning when several nodes
  are eligible.
- [x] Add configuration, resolver, policy, logging, and Home Manager acceptance
  tests for automatic discovery, explicit overrides, ambiguity, retry, replug,
  and multiple identical adapters.
- [x] Normalize libudev `/sys/devices/...` and PipeWire `/devices/...` sysfs
  paths before physical-adapter ownership checks, with realistic regression
  tests that preserve path-component boundaries.
- [x] Normalize PipeWire's udev-decorated `device.serial` before comparing it
  with the selected USB device's raw serial attribute.
- [x] Accept `wpctl inspect` structured values whose quoted payload contains
  unescaped inner quotes, without weakening required-property validation.
- [ ] Run the automatic-discovery Olympus release gates and record the matched
  HID, audio-device, sink, and source identities.

## 7. Logging and daemon lifecycle

- [x] Implement JSON-line structured logging and the required event names and
  fields.
- [x] Rate-limit repetitive unexpected HID and audio errors while logging
  unexpected HID recovery.
- [x] Treat HID errors classified as expected disconnect samples as normal
  debouncing input without HID failure/recovery logs.
- [x] Handle SIGINT/SIGTERM and close/cancel discovery, HID, retries, and child
  processes within the stop timeout.
- [x] Add lifecycle and log-schema tests.

## 8. Nix package and flake

- [x] Implement `packages/pslinkd/package.nix` with `buildGoModule`, cgo,
  libudev, tests, metadata, and a deterministic runtime path for `wpctl`.
- [x] Complete package/default outputs for `x86_64-linux` and `aarch64-linux`.
- [x] Keep the development environment exclusively in `devenv.nix`; export the
  formatter and package checks without a duplicate flake `devShells` output.
- [x] Verify `nix build`, `nix flake check`, and the packaged executable's
  runtime closure.

## 9. Home Manager module and host permission asset

- [x] Implement every typed `services.pslinkd` option and evaluation-time
  invariant in `specs/home-manager.md`.
- [x] Export `homeManagerModules.pslinkd` and `homeManagerModules.default`; do
  not export a pslinkd NixOS service module.
- [x] Add a compatible Home Manager flake input following the repository's
  nixpkgs input and use it for module evaluation checks.
- [x] Add the package to `home.packages` and generate strict YAML plus a
  systemd user unit with config/package `X-Restart-Triggers` for the owning
  account.
- [x] Install the scoped `054c:0ecc` hidraw udev rule in the package without
  trying to activate it from Home Manager.
- [x] Apply compatible unit hardening without hiding hidraw, the udev monitor,
  or the user PipeWire socket.
- [x] Add Home Manager evaluation/module tests for success and failure cases,
  including absence of system-level mutations.

## 10. Documentation and v1 hardware acceptance

- [x] Expand the README with standalone/Home Manager configuration, the NixOS
  host permission snippet and group-session caveat, JSON journal examples,
  known hardware, and non-goals.
- [ ] Run and record every automated acceptance criterion.
- [ ] Run and record every Olympus v1 release gate.
- [x] Publish the v1.1 button-interaction milestone and other outstanding
  post-v1 hardware-validation checklists.

## 11. V1.1 button interactions

- [x] Extend the HID report model with the three raw button bits, byte-43
  microphone polarity, and optional byte-44 volume; reject volume values above
  15 without rejecting the rest of the report.
- [x] Add a normalized interaction tracker that emits initial/changed
  volume/microphone state, detects independent rising button edges, and resets
  its baseline on debounced disconnect or adapter replacement.
- [x] Log normalized `volume_changed`, `microphone_state_changed`,
  `volume_up_pressed`, `volume_down_pressed`, and `microphone_mute_pressed`
  events, plus invalid volume reports.
- [x] Add strict `controls.enabled` YAML and typed
  `services.pslinkd.controls.enable` Home Manager options, both defaulting off
  and present in generated YAML.
- [x] Extend the wpctl adapter with freshly resolved `set-volume` and `set-mute`
  actions, including the linear `volume / 15` mapping for all 16 levels.
- [x] Fold routing and complete connected control state into one revisioned
  convergence loop so retries recover, newer reports cancel obsolete work,
  successful routes are not reasserted for control-only changes, and no stale
  controls apply after disconnect.
- [x] Add decoder/tracker, polling/logging, config, controller/wpctl, lifecycle,
  and Nix/Home Manager tests for every automated criterion in `specs/v1.1.md`.
- [ ] Run and record every Olympus v1.1 release gate before releasing v1.1.

## 11a. V1.1.1 host-only volume mode

- [x] Add strict `controls.volume_mode` YAML and typed
  `services.pslinkd.controls.volumeMode` Home Manager enums with
  `host-only` as the default and explicit `synchronized` compatibility mode.
- [x] Implement portable `HIDIOCSFEATURE(22)` support and the exact level-11
  `0xD0` device-volume payload without consuming the interrupt endpoint.
- [x] Add cancellable, coalesced device-volume convergence with report readback,
  retry, recovery logging, and lifecycle invalidation.
- [x] Add fresh headset-sink volume reads and idempotent absolute host-volume
  targets for `1 / 15` button steps, including pending-edge accumulation and
  clamping.
- [x] Keep raw absolute-volume logging while suppressing v1.1 absolute host
  convergence in `host-only` mode; leave microphone behavior unchanged.
- [x] Add every automated acceptance test in `specs/v1.1.1.md`.
- [ ] Run and record every Olympus v1.1.1 release gate before releasing v1.1.1.

## 12. V1.2 battery reporting

- [ ] Generalize the hidraw feature reader to request report `0x82` without
  changing the existing `0xB0` control-endpoint behavior.
- [ ] Decode the discrete battery level and derived percentage defined in
  `specs/v1.2.md`.
- [ ] Poll battery immediately after connection and every five seconds without
  feeding failures into connection debouncing or audio routing.
- [ ] Emit change-only `headset_battery_changed` and
  `headset_battery_unavailable` structured records.
- [ ] Add every automated acceptance test in `specs/v1.2.md`.
- [ ] Run and record every Olympus v1.2 release gate before releasing v1.2.

## 13. Other post-v1 validation and features

- [ ] Test restart, suspend/resume, auto-off behavior, and sustained audio
  monitoring.
- [ ] Design diagnostic tooling, config validation tooling, hooks, or IPC only
  through new specifications and acceptance criteria.
