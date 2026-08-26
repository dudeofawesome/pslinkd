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
  the README.

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

## 7. Logging and daemon lifecycle

- [x] Implement JSON-line structured logging and the required event names and
  fields.
- [x] Rate-limit repetitive expected HID/audio errors while logging recovery.
- [x] Handle SIGINT/SIGTERM and close/cancel discovery, HID, retries, and child
  processes within the stop timeout.
- [x] Add lifecycle and log-schema tests.

## 8. Nix package and flake

- [x] Implement `packages/pslinkd/package.nix` with `buildGoModule`, cgo,
  libudev, tests, metadata, and a deterministic runtime path for `wpctl`.
- [x] Complete package/default outputs for `x86_64-linux` and `aarch64-linux`.
- [x] Keep development tooling exclusively in `devenv.nix`; export the
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

- [ ] Expand the README with standalone/Home Manager configuration, the NixOS
  host permission snippet and group-session caveat, JSON journal examples,
  known hardware, and non-goals.
- [ ] Run and record every automated acceptance criterion.
- [ ] Run and record all eight Olympus v1 release gates.
- [ ] Publish the v1.1 button-interaction milestone and other outstanding
  post-v1 hardware-validation checklists.

## 11. V1.1 button interactions

- [ ] Decode volume-up, volume-down, and microphone-mute rising edges with
  correct baselining and normalized JSON events.
- [ ] Decode absolute volume and microphone state, including invalid-value
  handling and firmware-1.38 polarity fixtures.
- [ ] Add strict `controls.enabled` YAML and typed
  `services.pslinkd.controls.enable` Home Manager options, both defaulting off.
- [ ] Implement absolute PipeWire volume and microphone mute convergence using
  the v1 exact-name resolver and retry machinery.
- [ ] Add every automated acceptance test in `specs/v1.1.md`.
- [ ] Run and record every Olympus v1.1 release gate before releasing v1.1.

## 12. Other post-v1 validation and features

- [ ] Test restart, suspend/resume, auto-off behavior, and sustained audio
  monitoring.
- [ ] Design diagnostic tooling, config validation tooling, hooks, or IPC only
  through new specifications and acceptance criteria.
