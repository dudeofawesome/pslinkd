# Architecture

## Process model

pslinkd is one long-running Go process launched as a systemd user service. It
runs as the configured desktop user so `wpctl` connects to the same PipeWire and
WirePlumber session. A system-level udev rule grants that user access through a
dedicated group. That host-level authorization is a prerequisite outside the
Home Manager module; the daemon is never setuid and never starts as root.

The implementation MUST keep these boundaries independently testable:

```text
libudev discovery -> hidraw feature reader -> report decoder
                                         -> connection debouncer
                                         -> normalized state/events
                                         -> routing policy
                                         -> audio target resolver
                                         -> wpctl adapter
```

Discovery and HID polling MUST continue while `wpctl` subprocesses are running
or retrying. Audio failures MUST NOT terminate or stall hardware monitoring.

## Device discovery and lifecycle

Discovery uses `libudev` through a Go cgo binding. Startup performs an initial
enumeration of the `hidraw` subsystem. A monitor then consumes post-udev
add/remove events so the device node exists before an add is handled.

Candidates are matched by the fixed device profile in `requirements.md`,
including HID interface 3. Discovery produces a path only after all identity
checks pass. Each candidate also retains the parent USB-device syspath and USB
serial when present so audio discovery can identify the same physical adapter;
the interface-3 hidraw syspath itself MUST NOT be treated as the audio-device
syspath. Opening, polling, and closing the hidraw path belong to the HID layer.

The monitor MUST tolerate irrelevant and duplicate udev events. Monitor failure
MUST be logged and retried with backoff or cause a nonzero daemon exit so
systemd can restart it; it MUST NOT silently leave discovery inactive.

Physical removal immediately:

1. closes or invalidates the hidraw reader;
2. clears report state;
3. sets adapter presence and radio connection to false without radio debounce;
4. requests fallback routing; and
5. re-enumerates candidates.

## HID I/O

The daemon opens the selected hidraw character device read/write and issues
`HIDIOCGFEATURE(64)` with byte zero initialized to `0xB0`. The ioctl request
number MUST be computed using the Linux `_IOC` layout for the build
architecture, not copied as the x86_64 literal `0xC0404807`.

The implementation MUST use hidraw feature-report ioctl calls only. It MUST NOT
open a libusb interface, detach the kernel driver, read the hidraw interrupt
stream, or otherwise consume endpoint `0x81`.

V1.1.1 additionally uses `HIDIOCSFEATURE(22)` on the same read/write hidraw
descriptor for the exact masked device-volume write in `v1.1.1.md`. Feature
writes remain independently testable and MUST NOT delay report polling while
audio subprocesses run or retry.

The default poll interval is 200 ms and is configurable. V1.1.1 host-only
controls accelerate physical `0xB0` sampling to 50 ms while retaining the
configured interval for radio-failure debounce accounting, as defined in
`v1.1.1.md`. Poll cancellation and device removal MUST allow prompt shutdown
even if an ioctl is in progress.
Expected errors such as `EPIPE` are samples for connection debouncing, not
process-fatal errors. Errors classified by the HID layer as expected disconnect
samples (`EPIPE`, `ENODEV`, `EIO`, and `ETIMEDOUT`) MUST NOT emit HID failure or
recovery records. Unexpected open, read, and decode errors MUST emit
rate-limited failure records while preserving error type and recovery
transitions.

## Connection state machine

The default disconnect threshold is three consecutive unsuccessful samples,
equivalent to approximately 600 ms at the default interval.

- One successful report with the connection bit set transitions immediately to
  connected and resets the failure count.
- An ioctl error, malformed/wrong report, or successful report with a clear
  connection bit increments the consecutive failure count.
- While connected, fewer failures than the threshold retain connected state
  and cause no routing action.
- Reaching the threshold transitions once to disconnected.
- Adapter removal transitions immediately to disconnected.
- Starting without an adapter is immediately disconnected and requests the
  fallback route.
- Starting with an adapter but without a valid connected sample reaches
  disconnected through the normal threshold.

Routing actions occur only when the debounced state changes or initial state is
established. Polling MUST NOT set the default on every sample.

## Audio policy and wpctl adapter

V1 uses the `wpctl` command-line interface, not PulseAudio compatibility or
native PipeWire bindings. Fallback selectors and optional headset overrides are
exact PipeWire/WirePlumber `node.name` values. Ephemeral numeric IDs MUST NOT
appear in configuration or be retained across actions.

An explicit headset override is trusted as an exact selector and bypasses USB
association. For each such action, the adapter runs the machine-readable
`wpctl list audio sinks` or `wpctl list audio sources`, requires exactly one
exact name match, and uses that current ID with `wpctl set-default`.

When an override is absent, the resolver MUST:

1. use `wpctl list audio devices` and `wpctl inspect` to find the
   `Audio/Device` whose `device.sysfs.path` belongs to the selected HID
   candidate's parent USB-device syspath;
2. reject missing, malformed, or non-unique physical-device association rather
   than guessing from VID/PID alone; when serials are present on both sides, a
   mismatch also rejects the association; PipeWire's `device.serial` may use
   udev's `ID_SERIAL` form, so an underscore-delimited vendor/model prefix is
   ignored when comparing it with the USB device's raw serial attribute;
3. list and inspect the requested sink or source nodes and retain only nodes
   whose `device.id` references that matched audio device;
4. choose the node with the greatest integer `priority.session`, treating a
   missing priority as lower than every present priority and a present
   non-integer priority as malformed; and
5. break an equal-priority tie by lexicographically smallest `node.name`.

If several eligible nodes exist, the resolver MUST log a structured warning
listing their names, priorities, kind, and selected name even though selection
can continue. No eligible node, unparseable required properties, or an
unresolvable device association is an audio-action failure handled by the
normal retry policy. All device and node IDs MUST be resolved again on every
action and retry. Locale MUST NOT affect list or inspection parsing. Quoted
values containing the unescaped inner quotes emitted by `wpctl inspect` for
structured properties MUST NOT make otherwise usable inspection output fail.

On connection, desired routing is the automatically discovered headset sink or
its exact override. When source routing is enabled, connection likewise uses
the discovered headset source or its exact override. On disconnection or
adapter absence, routing uses the exact fallback sink and optional exact
fallback source. Headset targets need not be discovered for a fallback action.
WirePlumber's normal `linking.follow-default-target` behavior may move streams
following the default. pslinkd does not enumerate or forcibly move pinned
streams.

An audio transition creates a desired action revision. Failures—including no
match, ambiguous match, command timeout, nonzero exit, and WirePlumber absence—
MUST retain that revision and retry it with bounded exponential backoff. A newer
hardware transition cancels/replaces obsolete pending actions. Successful
actions are not periodically audited or repeated, allowing later user choices
to persist until the next transition or daemon start.

The initial retry delay SHOULD be 250 ms and the maximum SHOULD be 30 seconds.
Every subprocess MUST have a finite timeout and include useful stderr in a
rate-limited structured error.

## Shutdown and failure behavior

SIGINT and SIGTERM MUST stop new work, cancel retry timers/subprocesses, close
the hidraw device and udev monitor, and exit successfully within the systemd
stop timeout. Normal absence, radio disconnection, and `wpctl` unavailability
are not daemon-fatal.

Invalid configuration is fatal and MUST be reported before device or audio side
effects. Unrecoverable internal/discovery failures exit nonzero and rely on the
user unit's restart policy.
