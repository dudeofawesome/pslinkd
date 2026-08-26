# pslinkd v1 specification

This directory is the authoritative specification for pslinkd. `handoff.md`
records the hardware research that informed these requirements; where the
handoff offers alternatives, the specifications in this directory are the
chosen design.

## Documents

- [requirements.md](requirements.md): product behavior, supported hardware,
  and scope.
- [architecture.md](architecture.md): process boundaries, state machine, HID
  access, and WirePlumber integration.
- [configuration.md](configuration.md): command line and strict YAML schema.
- [home-manager.md](home-manager.md): Nix package, Home Manager module,
  systemd user service, and host permission boundary.
- [testing.md](testing.md): automated and Olympus hardware acceptance
  criteria.
- [v1.1.md](v1.1.md): deferred button, volume, and microphone interactions.

## Requirement language

The words **MUST**, **MUST NOT**, **SHOULD**, and **MAY** are normative. V1 is
complete only when every automated acceptance criterion and every v1 hardware
release gate in [testing.md](testing.md) passes.

## V1 scope

V1 is a single per-user Go daemon which:

- detects a Sony PULSE Elite CFI-ZWA2 PlayStation Link adapter;
- polls feature report `0xB0` without consuming an interrupt endpoint;
- debounces the headset radio-link state;
- changes WirePlumber's default output, and optionally its default input, on
  radio-link transitions;
- is packaged and deployable through Nix and a Home Manager module without
  running as root.

V1 intentionally has no D-Bus or socket API, event-command hooks, forced stream
migration, configuration reload, diagnostic subcommand, button interaction,
volume/microphone synchronization, or support for unknown PlayStation Link
adapter models.

## V1.1 scope

V1.1 adds the complete button-interaction vertical slice defined in
[v1.1.md](v1.1.md). V1 implementation and release criteria MUST NOT depend on
that deferred functionality.
