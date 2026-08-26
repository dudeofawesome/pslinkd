# Nix package and Home Manager module

## Ownership boundary

pslinkd runs entirely in one user's audio session, so declarative service
deployment belongs to Home Manager. The Home Manager module owns the package,
strict YAML configuration, and systemd user service.

Hidraw authorization remains a host-administrator responsibility. Home Manager
cannot create a system group or activate a system udev rule and MUST NOT claim
that enabling this module alone grants device access.

## Flake outputs

The flake MUST pin a Home Manager input whose nixpkgs input follows this
flake's nixpkgs input, so module evaluation checks use a compatible module
system and package set.

The flake MUST retain Linux packages for `x86_64-linux` and `aarch64-linux` and
export:

- `packages.<system>.pslinkd` and `packages.<system>.default`;
- `homeManagerModules.pslinkd` and `homeManagerModules.default`;
- checks that build/test Go and evaluate the Home Manager module.

It MUST NOT export pslinkd as a NixOS service module.

The development environment, including Go/cgo, libudev, WirePlumber, and Nix
tools, MUST be defined only by `devenv.nix`. The flake MUST NOT export
`devShells`, so the repository has one development-environment definition.

## Package

The package uses `buildGoModule` with cgo enabled. Build inputs MUST provide
`libudev` headers and libraries. The runtime closure MUST contain libudev and
`wpctl`; invoking the packaged executable outside the module must not depend on
an ambient, mutable `PATH` to locate `wpctl`.

The package MUST run Go tests during its build and install the `pslinkd`
executable. Package metadata MUST identify Linux-only support and the repository
license.

The package MUST also install a udev rule under `lib/udev/rules.d` scoped to
hidraw devices belonging to USB `054c:0ecc`. The rule grants group `pslink`
read/write access with mode `0660`; it MUST NOT match every Sony device or every
hidraw device. Installing the package in a Home Manager profile does not
activate this rule.

## Home Manager module API

The module namespace is `services.pslinkd` and exposes typed options:

| Option | Type/default | Meaning |
| --- | --- | --- |
| `enable` | boolean, `false` | Enable user deployment |
| `package` | package | pslinkd package to install and run |
| `audio.headsetSink` | null or nonempty string, `null` | Exact headset `node.name` override; null discovers automatically |
| `audio.fallbackSink` | nonempty string, required | Exact fallback `node.name` |
| `audio.headsetSource` | null or nonempty string, `null` | Exact headset source override; null discovers when source routing is enabled |
| `audio.fallbackSource` | null or nonempty string, `null` | Exact fallback source; non-null enables source routing |
| `polling.interval` | duration string, `200ms` | Feature-report interval |
| `polling.disconnectFailures` | integer, `3` | Consecutive failures to disconnect |
| `logLevel` | enum, `info` | JSON log threshold |

There is no username option: the owner is the account whose Home Manager
configuration imports and enables the module.

The module MUST enforce the same bounds and source invariant as the daemon at
Home Manager evaluation time: a non-null `headsetSource` requires a non-null
`fallbackSource`, while `fallbackSource` alone enables automatic headset-source
discovery. Null headset selectors MUST be omitted from generated YAML. The
module adds the selected package to `home.packages`, generates strict YAML in
the Nix store, and passes its path explicitly with `pslinkd run --config ...`.
The module MUST declare the generated config and selected package in the unit's
`X-Restart-Triggers`, so normal Home Manager `sd-switch` activation restarts the
service when either changes. It MUST NOT override the user's global
`systemd.user.startServices` policy.

Button interaction options are reserved for v1.1 and MUST NOT appear in the v1
module API or generated configuration.

The module MUST NOT enable, configure, or attempt to assert NixOS PipeWire,
WirePlumber, users, groups, udev, or lingering options. Its documentation MUST
state that a running PipeWire/WirePlumber user session and `wpctl` compatibility
are runtime prerequisites.

The module MUST assert that it is evaluated for Linux.

## systemd user service

The module installs `systemd.user.services.pslinkd` in the owning account. It
starts in that account's normal user service lifecycle, wants and orders itself
after `wireplumber.service`, and is wanted by the ordinary user target.

The module MUST NOT enable systemd lingering or create a system-level service.
It does not need `ConditionUser` because Home Manager installs the unit only for
the account that enabled it.

The unit:

- executes the selected package and generated config;
- restarts on unexpected failure with a bounded restart delay;
- sends SIGTERM and allows graceful shutdown;
- receives `wpctl` from the immutable package/runtime closure;
- applies compatible user-unit hardening, including no-new-privileges; and
- retains access to the user PipeWire socket, libudev netlink monitoring, the
  selected hidraw node, and its read-only configuration.

Hardening MUST NOT use a private device namespace that hides hidraw.

## Host device-access prerequisite

Before the Home Manager service can poll the adapter, a host administrator MUST:

1. create the system group `pslink`;
2. add the Home Manager user to that group;
3. activate the scoped udev rule shipped by the package; and
4. recreate the login session after changing supplementary group membership.

For NixOS, documentation MUST provide a small host-configuration example using
`users.groups.pslink.members` and `services.udev.packages` with the pslinkd
package. This is host integration guidance, not a pslinkd NixOS module.

The daemon MUST report permission-denied errors clearly and remain non-root. It
MUST NOT invoke sudo or attempt to repair system permissions itself.
