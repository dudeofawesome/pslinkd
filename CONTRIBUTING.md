# Contributing to pslinkd

## Development environment

The repository uses devenv as its only development environment. It supplies Go
1.25, cgo, libudev on Linux, WirePlumber, Nix, and the Nix formatter. Run
project commands through it:

```console
devenv shell --quiet -- go test ./...
devenv shell --quiet -- gofmt -w .
devenv shell --quiet -- go vet ./...
```

The `check` script runs formatting, tests, and vet together:

```console
devenv shell --quiet -- check
```

Flake checks cover the Go package, Home Manager evaluation and failure cases,
the rendered user unit and YAML, restart triggers, and the scoped udev rule.
Evaluate all declared systems with:

```console
nix flake check --all-systems --no-build
```

Omit `--no-build` when builders for both supported Linux systems are
configured.

## Spec-driven changes

The authoritative behavior, architecture, and acceptance criteria live in
[`specs`](specs). Before changing behavior:

1. Read the specifications.
2. Update the relevant specification and acceptance criteria.
3. Update [`tasks.md`](tasks.md) to reflect the implementation work.
4. Implement the change and add automated tests for its acceptance criteria.
5. Run the relevant checks.

Do not introduce behavior that is absent from the specifications. Keep hardware
access, timing, process execution, and discovery behind testable interfaces so
the automated suite does not require physical hardware, root, udev, or a live
PipeWire server.

## Design boundaries

Changes need a specification update before they expand these boundaries:

- do not read or detach the adapter's USB interrupt endpoint;
- do not force-move pinned streams or periodically enforce an audio default;
- do not run arbitrary hooks or expose an IPC API;
- do not manage several adapters concurrently;
- do not configure PipeWire, WirePlumber, users, groups, udev, or lingering;
  and
- do not accept configurable or unvalidated USB device IDs.

The current control synchronization uses absolute headset volume and
microphone state. Relative host-volume button behavior belongs to the proposed
host-only volume milestone described in [`CHANGELOG.md`](CHANGELOG.md).
