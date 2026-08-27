# Testing and acceptance

## Automated test strategy

Hardware, time, process execution, and discovery MUST sit behind interfaces so
most behavior can be tested without a Sony adapter, root, udev, or a running
PipeWire server. Tests SHOULD use a fake clock and scripted report/audio
adapters instead of sleeping.

### Configuration tests

Automated tests MUST cover:

- the canonical minimal automatic-sink config, exact sink override, automatic
  source config, and exact source override;
- defaults for polling, disconnect threshold, and log level;
- comments in YAML;
- unknown and duplicate keys;
- missing/empty fallback sink and empty headset overrides;
- a headset source without a fallback source;
- invalid duration, bounds, integer, and log level; and
- XDG default-path resolution plus `--config` override.

### HID and decoder tests

Fixtures MUST cover the known 64-byte report layout, the connection field,
wrong report ID/length, and ioctl error classification.
The computed ioctl value MUST equal `0xC0404807` on supported architectures
whose Linux ABI defines that value.

V1 tests MUST demonstrate that button, volume, and microphone report fields do
not produce normalized events or audio actions.

### State-machine tests

Using a fake clock and sample stream, tests MUST prove:

- immediate connection on one valid connected report;
- no disconnect or route change after one or two failures at defaults;
- one disconnect after the third consecutive failure;
- a successful connected sample resets the failure count;
- clear connection bits and ioctl failures use the same threshold;
- physical removal disconnects immediately;
- startup without an adapter requests fallback immediately;
- startup with a powered-off adapter requests fallback after the threshold;
- replug discovers and polls a new hidraw path; and
- multiple matches select the lexicographically first syspath and warn.

### Policy and wpctl tests

Tests MUST use a fake command runner and realistic machine-readable `wpctl list`
and `wpctl inspect` fixtures. They MUST cover:

- exact-name resolution and current-ID use;
- missing and ambiguous targets;
- correlation of a selected HID USB parent with its PipeWire audio device and
  rejection of a same-VID/PID device belonging to another physical adapter;
- absent, malformed, non-unique, and serial-mismatched device association;
- automatic sink and source selection through the matched `device.id`;
- highest-`priority.session` selection, missing priorities, lexicographic
  name tie-breaking, and structured warnings listing multiple eligible nodes;
- fresh device/node resolution on every action and retry without retained IDs;
- automatic and exact-override headset/fallback sink transitions;
- source routing enabled by fallback source alone and exact headset-source
  overrides;
- no source commands when fallback source is absent;
- subprocess error, timeout, bounded-backoff retry, recovery, and cancellation
  of obsolete desired actions;
- no repeated default action on steady HID samples; and
- no periodic correction after a successful action, preserving a simulated
  later user selection until the next hardware transition.

No test or production path may invoke `pactl` or a stream-move command.

### Lifecycle and log tests

Tests MUST cover graceful cancellation of polling, retry timers, and
subprocesses; recovery/failure behavior of the discovery monitor; and valid
one-object-per-line JSON for all required state/action events. Expected HID
disconnect errors MUST contribute failure samples without emitting
`hid_failure` or `hid_recovered`; unexpected HID failures and repeated audio
failures MUST demonstrate log rate limiting, and unexpected HID recovery MUST
be logged.

### Nix tests

Flake checks MUST at minimum:

- build the Go package and run its tests;
- evaluate a valid Home Manager module configuration;
- accept null automatic headset selectors and fallback-source-only automatic
  source routing;
- reject a missing fallback sink, headset source without fallback source, and
  invalid timing;
- inspect the rendered user unit, generated YAML, and installed home package;
- verify config/package restart triggers without overriding
  `systemd.user.startServices`;
- inspect the package's scoped udev rule;
- demonstrate that the module defines no system service, system group, udev,
  user-selection, or lingering configuration; and
- build both declared Linux package outputs when builders are available.

## V1 Olympus release gates

These manual tests on Olympus firmware 1.38 MUST pass before v1 is called
complete:

1. Start with the adapter absent: the configured fallback sink is selected.
2. Insert the adapter with the headset off: the fallback remains selected and
   the daemon stays healthy despite expected feature-report failures.
3. Power on the headset: the automatically discovered Sony sink becomes
   default promptly.
4. Inject or observe one and two transient failed reads while connected: the
   Sony route remains selected.
5. Power off the headset: the fallback becomes default after approximately
   three 200 ms failures, without repeated route flapping.
6. Remove and reinsert the adapter, with the headset tested both off and on:
   discovery resumes on the current hidraw path without restarting pslinkd.
7. Change the default output manually in GNOME after a completed pslinkd
   transition: pslinkd does not overwrite it until the next headset transition.
8. Confirm the Home Manager service runs as its owning non-root user and can
   access only the scoped device after the host `pslink` group/rule prerequisite
   is configured.
9. Configure exact headset sink and source overrides and confirm they bypass
   automatic USB association while still resolving current node IDs.
10. Configure a fallback source without a headset-source override. Confirm the
    automatically resolved sink and source both belong to the same physical
    USB adapter as the selected interface-3 hidraw device.

## V1.1 button interactions

Button-edge decoding, absolute volume and microphone state, control
synchronization, and their hardware gates are specified in `v1.1.md`. They do
not block v1.

## V1.2 battery reporting

Feature-report `0x82` decoding, change-only structured battery records, and the
firmware-1.38 hardware gates are specified in `v1.2.md`. They do not block v1
or v1.1.

## Other tracked post-v1 hardware validation

These follow-up tasks are not button interactions and do not block the
core-routing v1 release:

- test service restart with the headset on and off;
- test suspend/resume recovery;
- observe whether continuous 5 Hz polling changes headset auto-off behavior;
- play sustained audio and confirm monitoring causes no USB or PipeWire
  disruption.
