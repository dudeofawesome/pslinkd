# PULSE Elite Linux Service — Agent Handoff

## Objective

Build a standalone Linux service that monitors a Sony PULSE Elite headset through
its PlayStation Link USB adapter and reacts to:

- headset connection and disconnection;
- volume-up, volume-down, and microphone-mute button presses;
- absolute headset volume and microphone mute state;
- audio-output routing between the headset and a fallback output.

This service should live in its own repository. Do not implement it directly in
the `dudeofawesome/nix-config` repository. NixOS packaging and module integration
can be added there later, after the service has a stable interface.

## Verified target system

Host: `olympus`, running NixOS with PipeWire and WirePlumber.

The adapter currently attached to Olympus is:

```text
USB vendor/product: 054c:0ecc
Product: Sony Interactive Entertainment PlayStation Link Adapter
Model family: CFI-ZWA2
Firmware/bcdDevice: 1.38
USB HID interface: interface 3
Linux driver: hid-generic/usbhid
Current hidraw node: /dev/hidraw0
```

Do not hard-code `/dev/hidraw0`; discover the device by VID/PID and, where
needed, HID interface number. The hidraw number can change after reboot or when
other devices are attached.

The stable udev link observed on Olympus was:

```text
/dev/input/by-id/usb-Sony_Interactive_Entertainment_PlayStation_Link_Adapter_901c131a-6085-0492-06ec-d05027810150-if03-hidraw
```

Prefer VID/PID discovery over hard-coding this serial-specific link so the
service remains portable to replacement adapters.

## PipeWire state observed on Olympus

PipeWire, WirePlumber, and PipeWire Pulse compatibility were all active.

Headset sink:

```text
alsa_output.usb-Sony_Interactive_Entertainment_PlayStation_Link_Adapter_901c131a-6085-0492-06ec-d05027810150-00.analog-stereo
```

Headset source:

```text
alsa_input.usb-Sony_Interactive_Entertainment_PlayStation_Link_Adapter_901c131a-6085-0492-06ec-d05027810150-00.mono-fallback
```

Fallback HDMI sink used during inspection:

```text
alsa_output.pci-0000_03_00.1.hdmi-surround-extra3
```

The Sony sink was the saved WirePlumber default even while the physical headset
was powered off. This is the original problem: the USB audio device represents
the always-connected dongle, not the radio link between dongle and headset.

Sink names should be configurable. Avoid embedding the Olympus serial number or
PCI address as application constants.

## HID protocol findings

The adapter exposes HID feature report `0xB0`. Its declared report size is 64
bytes including the report ID. Linux can request it through `HIDIOCGFEATURE` on
the hidraw device. This goes through USB endpoint 0 as a control transfer.

Relevant fields in report `0xB0`:

| Offset | Mask/value | Meaning |
| --- | --- | --- |
| Byte 39 | `0x01` | Headset radio link is connected |
| Byte 39 | `0x08` | Volume-up button is pressed |
| Byte 39 | `0x10` | Volume-down button is pressed |
| Byte 39 | `0x20` | Microphone-mute button is pressed |
| Byte 43 | high nibble | Microphone mute state; see validation note below |
| Byte 44 | `0..15` | Absolute headset volume |

### Connection behavior verified on Olympus

With the headset powered on and the adapter indicator solid white:

```text
report=b0 byte39=01 connected=True
```

With the headset powered off, firmware 1.38 stalls the feature request and Linux
returns `EPIPE`/`BrokenPipeError` rather than returning a report whose connection
bit is zero.

Therefore, for this firmware:

```text
successful 0xB0 read and (report[39] & 0x01) != 0 => connected
EPIPE, failed read, or connection bit clear           => disconnected
```

Do not treat every single transient I/O error as an immediate disconnect in the
finished daemon. Use a short failure threshold or debounce window, while still
making normal power-off routing feel prompt. Device removal should be handled
separately from a radio-link disconnect.

### Button handling

Poll report `0xB0` at approximately 5 Hz initially. Detect button events on the
rising edge of the corresponding bit rather than firing continuously while a
button is held.

For volume, also compare byte 44 between samples. An absolute volume change can
confirm the operation even if a very brief button bit is missed between polls.

The mic-state decoding seen in the reverse-engineered implementation is:

```text
(report[43] & 0xF0) == 0x00 => muted
(report[43] & 0xF0) != 0x00 => active
```

Validate this mapping against Olympus before making it part of the stable API,
because an older prototype used a narrower equality test and Olympus runs older
firmware than the published implementation.

## Important USB constraint

Avoid opening or consuming interrupt endpoint `0x81` directly. Reverse
engineering of CFI-ZWA2 firmware 1.43 found that state changes can cause the
adapter to send a malformed 256-byte burst through a 64-byte interrupt endpoint.
On Windows this causes USB babble recovery and can tear down audio.

Use `GET_REPORT(Feature 0xB0)` over endpoint 0 instead. This is the approach used
by the existing reverse-engineered companion application. Olympus is on firmware
1.38, so behavior should still be tested under sustained audio and repeated
button presses.

The published implementation polls at roughly 5 Hz and describes that poll as a
host-presence keepalive. Determine whether continuous polling changes headset
auto-off behavior on Linux and document the result.

## Linux access and permissions

During inspection, `/dev/hidraw0` was:

```text
crw------- root root /dev/hidraw0
```

It carried the udev `uaccess` tag, but the active user had no effective ACL.
Inspection therefore used passwordless sudo.

The deployed service should not run as root merely to read the device. Add a
narrow udev rule matching only this adapter, then grant the daemon user or a
dedicated group read/write access to its hidraw node. Example direction, to be
refined during packaging:

```udev
SUBSYSTEM=="hidraw", ATTRS{idVendor}=="054c", ATTRS{idProduct}=="0ecc", \
  GROUP="pslink", MODE="0660"
```

If implemented as a per-user service, consider `TAG+="uaccess"` and verify that
seat/session ACLs work under the Gamescope/Jovian session used by Olympus. A
dedicated system daemon plus an IPC interface may be more reliable if routing
must work before the graphical user session is fully initialized.

## Suggested service architecture

Keep hardware monitoring separate from routing policy:

```text
Device discovery
  -> HID feature-report polling
  -> normalized events/state
  -> configurable policy/actions
  -> PipeWire/WirePlumber adapter
```

Suggested normalized state:

```text
adapter_present: bool
headset_connected: bool
volume: optional integer 0..15
mic_muted: optional bool
volume_up_pressed: bool
volume_down_pressed: bool
mute_pressed: bool
```

Suggested events:

```text
adapter-added
adapter-removed
headset-connected
headset-disconnected
volume-changed
volume-up-pressed
volume-down-pressed
mute-pressed
mic-state-changed
```

Configuration should include:

- USB VID/PID, with `054c:0ecc` as the initial supported device;
- preferred headset sink and source selectors;
- fallback sink and optional fallback source selectors;
- polling interval and disconnect debounce;
- whether to move already-running streams;
- optional commands/hooks for button events;
- log level and a diagnostic mode that dumps decoded state without routing.

Support for the newer adapter PID `054c:0fa3` must not be assumed. Its HID layout
is reported to differ and has not been reverse-engineered by the referenced
project.

## Audio-routing behavior

Minimum desired policy:

```text
headset connected    -> make Sony headset the default output
headset disconnected -> make configured HDMI output the default output
```

`wpctl set-default <node-id>` changes the default target for new streams and
WirePlumber remembers the selection. Node IDs are ephemeral, so resolve the
configured stable node name to the current ID at action time.

Alternatively, the PipeWire Pulse compatibility API allows stable names with:

```sh
pactl set-default-sink <sink-name>
```

Changing the default does not necessarily migrate existing game or Steam audio
streams. If configured to do so, enumerate active sink inputs and move them to
the new sink. Make this behavior optional because users may deliberately pin an
application to another output.

Avoid repeatedly setting the same default on every poll. Act only on a debounced
state transition.

## Implementation-language considerations

Reasonable choices include Rust, Go, or Python:

- Rust offers strong system-daemon packaging and direct hidraw/ioctl support.
- Go is convenient for a small static daemon if a suitable HID library exposes
  feature reports correctly on Linux.
- Python is fastest for a protocol prototype, using `hidapi` or `fcntl.ioctl`,
  but requires packaging its runtime and HID bindings.

Whichever language is selected, verify that its HID library uses feature reports
over the control endpoint and does not start consuming the interrupt endpoint as
a side effect. A direct hidraw `HIDIOCGFEATURE` implementation gives the clearest
control over this behavior.

## Reproduction code used during inspection

The following Python logic was executed as root on Olympus. The ioctl number is
for a 64-byte `HIDIOCGFEATURE` buffer on the target x86_64 Linux system:

```python
import fcntl
import os

report = bytearray(64)
report[0] = 0xB0

fd = os.open("/dev/hidraw0", os.O_RDWR)
fcntl.ioctl(fd, 0xC0404807, report, True)

connected = bool(report[39] & 0x01)
print(f"report={report[0]:02x} byte39={report[39]:02x} connected={connected}")
```

Production code should compute/use the platform's `HIDIOCGFEATURE(64)` macro
rather than assuming a hard-coded ioctl value is portable across architectures.
It must also discover the correct hidraw device rather than using
`/dev/hidraw0`.

## Recommended milestones

1. Build a diagnostic CLI that discovers `054c:0ecc`, polls `0xB0`, and prints
   decoded state transitions.
2. Confirm all three button bits and byte 44 interactively on Olympus.
3. Validate byte 43 mic-state decoding on firmware 1.38.
4. Test disconnect debounce, adapter removal/reinsertion, suspend/resume, and
   service restart while the headset is on and off.
5. Play continuous audio while repeatedly pressing buttons; confirm monitoring
   does not destabilize PipeWire or USB audio.
6. Add configurable PipeWire routing and optional migration of existing streams.
7. Add systemd service definitions, least-privilege device access, logs, and
   graceful shutdown.
8. Package the service for Nix, then integrate it into Olympus from the separate
   Nix configuration repository.

## Acceptance criteria

- The dongle may remain permanently attached.
- Headset power-on selects it as the configured default output promptly.
- Headset power-off selects the fallback output promptly and reliably.
- Brief USB errors do not cause route flapping.
- Volume and mute button presses generate one normalized event per press.
- Absolute volume and mic state are observable.
- No hard-coded hidraw number or ephemeral PipeWire node ID is required.
- The daemon runs without root after installation.
- Adapter replug and system suspend/resume recover automatically.
- Monitoring does not disrupt audio during sustained playback.

## References

- Reverse-engineered protocol and Windows companion:
  <https://github.com/Jprnp/pslink-libusb>
- Linux hidraw feature-report API:
  <https://docs.kernel.org/hid/hidraw.html>
- WirePlumber `wpctl` documentation:
  <https://pipewire.pages.freedesktop.org/wireplumber/man/wpctl.html>
- Sony connection/status-light documentation:
  <https://www.playstation.com/en-us/support/hardware/connect-pulse-elite/>

## Inspection summary

Read-only commands used on Olympus included:

- `lsusb -d 054c:` and `lsusb -v -d 054c:0ecc`;
- `wpctl status -n`;
- `udevadm info --query=property --name=/dev/hidraw0`;
- reading the sysfs HID report descriptor;
- a Python `HIDIOCGFEATURE(64)` request for report `0xB0` with the headset
  explicitly tested once on and once off.

No Olympus configuration, audio routing, device permissions, or repository files
were changed during investigation.
