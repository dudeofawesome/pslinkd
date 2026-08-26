# pslinkd

pslinkd is a per-user Linux daemon that switches the WirePlumber default audio
route when a Sony PULSE Elite headset connects to or disconnects from its
PlayStation Link adapter.

The USB audio device remains present while the headset is powered off, so
pslinkd reads the adapter's HID radio-link state instead of treating USB audio
presence as headset presence. It changes the default sink and, optionally, the
default source. It does not move pinned streams or repeatedly overwrite later
user choices.

## Supported hardware

V1 supports exactly this device profile:

|                |                             |
| -------------- | --------------------------- |
| Headset        | Sony PULSE Elite / CFI-ZWA2 |
| USB adapter    | `054c:0ecc`                 |
| HID interface  | `3`                         |
| Feature report | `0xB0`, 64 bytes            |

Other adapters are ignored, including Sony `054c:0fa3`. Device paths and
serial numbers are discovered dynamically. If several supported adapters are
present, pslinkd selects the one with the lexicographically first libudev
syspath and logs a warning.

## Runtime prerequisites

pslinkd requires:

- Linux with hidraw and libudev;
- a running PipeWire/WirePlumber user session with `wpctl` compatibility
- host-level permission for the service user to read and write the supported
  adapter's hidraw node.

The Nix package includes libudev linkage, `wpctl` in its immutable runtime
path, and a scoped udev rule. Installing the package in a user profile does not
activate that system-level rule.

## Find audio node names

Configuration uses exact PipeWire/WirePlumber `node.name` values, not the
numeric IDs that can change between actions. List the available values with:

```console
wpctl list audio sinks
wpctl list audio sources
```

Choose one headset and one fallback sink. Source routing is optional, but its
headset and fallback values must be configured together.

## Home Manager

Add pslinkd to the inputs of the flake that owns your Home Manager
configuration:

```nix
{
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

    pslinkd = {
      url = "github:dudeofawesome/pslinkd";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };
}
```

Import the module in the user's Home Manager configuration. This example
assumes the flake inputs are passed to Home Manager with
`extraSpecialArgs = { inherit inputs; };`:

```nix
{ inputs, ... }:

{
  imports = [ inputs.pslinkd.homeManagerModules.default ];

  services.pslinkd = {
    enable = true;

    audio = {
      fallbackSink = "alsa_output.pci-0000_03_00.1.hdmi-surround-extra3";

      # Optional exact-name overrides; null discovers from the selected adapter.
      # headsetSink = "alsa_output.usb-Sony_Interactive_Entertainment_PlayStation_Link_Adapter_SERIAL-00.analog-stereo";
      # headsetSource = "alsa_input.usb-Sony_Interactive_Entertainment_PlayStation_Link_Adapter_SERIAL-00.mono-fallback";

      # Optional; enables source routing and automatic headset-source discovery.
      fallbackSource = "alsa_input.pci-0000_00_1f.3.analog-stereo";
    };

    polling = {
      interval = "200ms";
      disconnectFailures = 3;
    };

    logLevel = "info";
  };
}
```

The module installs the selected package, generates strict YAML in the Nix
store, and creates `pslinkd.service` in the owning user's normal systemd
lifecycle. Configuration and package changes are service restart triggers. The
module does not change the user's global `systemd.user.startServices` policy.

### NixOS device permission

Host authorization is required in addition to the Home Manager module. A
NixOS administrator can create the dedicated group, add the user, and activate
the package's scoped udev rule with the following host module. This example
likewise assumes `inputs` is available as a module argument:

```nix
{ inputs, pkgs, ... }:

let
  pslinkdPackage =
    inputs.pslinkd.packages.${pkgs.stdenv.hostPlatform.system}.pslinkd;
in
{
  users.groups.pslink.members = [ "alice" ];
  services.udev.packages = [ pslinkdPackage ];
}
```

Replace `alice` with the Home Manager account name. After applying the host
configuration, fully end and recreate that user's login session so the new
supplementary group membership takes effect. Reconnecting the adapter ensures
the active rule is applied to its current hidraw node.

This is host integration, not a NixOS service module. pslinkd remains a
non-root user process and never invokes `sudo` or attempts to repair system
permissions.

## Standalone use

Build the Linux package directly from the flake:

```console
nix build github:dudeofawesome/pslinkd
```

Create `config.yaml`:

```yaml
audio:
    fallback_sink: alsa_output.pci-0000_03_00.1.hdmi-surround-extra3

    # Optional exact-name overrides. Omit them to discover nodes belonging to
    # the selected physical USB adapter.
    # headset_sink: alsa_output.usb-Sony_Interactive_Entertainment_PlayStation_Link_Adapter_SERIAL-00.analog-stereo
    # headset_source: alsa_input.usb-Sony_Interactive_Entertainment_PlayStation_Link_Adapter_SERIAL-00.mono-fallback

    # Optional; enables source routing and automatic headset-source discovery.
    # fallback_source: alsa_input.pci-0000_00_1f.3.analog-stereo

polling:
    interval: 200ms
    disconnect_failures: 3

logging:
    level: info
```

Run it in the same user session as WirePlumber:

```console
./result/bin/pslinkd run --config ./config.yaml
```

Without `--config`, pslinkd reads
`$XDG_CONFIG_HOME/pslinkd/config.yaml`, falling back to the platform's standard
user configuration directory. The document is strict YAML: unknown or
duplicate keys, invalid types, a missing fallback sink, a headset-source
override without a fallback source, and out-of-range polling values are errors.

Standalone use still requires an administrator to create the `pslink` group,
add the user, and activate the udev rule shipped at
`lib/udev/rules.d/70-pslinkd.rules` in the package output.

## Service and logs

Inspect or follow the Home Manager service with:

```console
systemctl --user status pslinkd.service
journalctl --user -u pslinkd.service -f -o cat
```

Every daemon record is one JSON object containing at least `time`, `level`,
`event`, and `message`. Representative records look like:

```json
{"time":"2026-08-26T18:00:00Z","level":"info","event":"daemon_start","message":"pslinkd started"}
{"time":"2026-08-26T18:00:01Z","level":"info","event":"adapter_added","message":"PlayStation Link adapter added","device_path":"/dev/hidraw4","adapter_present":true}
{"time":"2026-08-26T18:00:02Z","level":"info","event":"headset_connected","message":"headset radio link connected","adapter_present":true,"headset_connected":true}
{"time":"2026-08-26T18:00:02Z","level":"info","event":"audio_action_succeeded","message":"audio defaults updated","attempt":1,"revision":2,"target_name":"alsa_output.usb-Sony_Interactive_Entertainment_PlayStation_Link_Adapter_SERIAL-00.analog-stereo"}
```

Expected HID and audio failures are rate-limited. Audio failures retain the
desired transition and retry with bounded backoff without stopping device
monitoring. Permission errors include the hidraw path and error type; verify
the active udev rule, `pslink` membership, and recreated login session when
diagnosing them.

If an audio target cannot be resolved, compare `target_name` in the retry log
with the exact `node.name` reported by `wpctl list`. Numeric IDs must not be put
in configuration.

## V1 behavior and non-goals

At the defaults, one connected report selects the headset route immediately.
Three consecutive unsuccessful 200 ms samples select the fallback route.
Adapter removal falls back immediately, and reinsertion is discovered without
restarting the daemon.

After pslinkd successfully handles a transition, a later default selected in
GNOME or another client is left alone until the next headset transition or
daemon start.

V1 does not:

- read or detach the adapter's USB interrupt endpoint;
- force-move pinned streams;
- periodically enforce a default route;
- handle volume, microphone, or headset-button state;
- run arbitrary hooks or expose an IPC API;
- manage several adapters concurrently;
- configure PipeWire, WirePlumber, users, groups, udev, or lingering; or
- support configurable or unvalidated USB device IDs.

## Development

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
Build the checks for an available Linux builder and evaluate all declared
systems with:

```console
nix flake check --all-systems --no-build
```

Use `x86_64-linux` for the first command when that is the available builder.
Omit `--no-build` from the second command when builders for both Linux systems
are configured.

The authoritative behavior and acceptance criteria live in [`specs`](specs).
