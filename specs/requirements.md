# Product requirements

## Goals

The USB adapter remains present as an audio device when the physical headset is
powered off. pslinkd MUST use the adapter's HID radio-link state, rather than
USB audio-device presence, to choose an audio route.

When the configured user session is running:

1. A connected headset MUST select the configured headset sink promptly.
2. A disconnected headset or absent adapter MUST select the configured
   fallback sink promptly.
3. When both optional sources are configured, connection MUST select the
   headset source and disconnection MUST select the fallback source.
4. Transient HID failures below the disconnect threshold MUST NOT flap the
   audio route.
5. The daemon MUST recover from adapter removal and reinsertion without being
   restarted.
6. A user selection made in GNOME or another audio client after pslinkd has
   successfully handled a transition MUST be left alone until the next headset
   transition or daemon start.
7. The daemon MUST run without root privileges.

## Supported hardware

V1 supports exactly this device profile:

| Field | Value |
| --- | --- |
| Product family | Sony PULSE Elite / CFI-ZWA2 |
| USB vendor ID | `054c` |
| USB product ID | `0ecc` |
| HID interface | `3` |
| Feature report | `0xB0` |
| Report length | 64 bytes including report ID |

The daemon MUST ignore other VID/PID pairs. In particular, it MUST NOT treat
`054c:0fa3` as compatible. Additional adapters require separately validated
device profiles; USB IDs are not configurable in v1.

The hidraw number and device serial MUST NOT be hard-coded. If more than one
supported adapter is present, the daemon MUST select the adapter with the
lexicographically first libudev syspath and log one warning listing all
candidates. Selection MUST be recomputed when the selected adapter is removed.

## Connection decoding

A successful v1 report decodes only the radio-link field:

| Input | Meaning |
| --- | --- |
| `report[39] & 0x01 != 0` | radio link connected |

Normalized v1 state consists only of adapter presence and debounced radio
connection. Volume buttons, microphone-mute buttons, absolute volume,
microphone mute state, related events, and related audio controls are reserved
for v1.1 and defined in `v1.1.md`.

## Non-goals

V1 MUST NOT:

- consume USB interrupt endpoint `0x81`;
- force-move streams that a user or application pinned to a target;
- periodically overwrite a user-selected default merely because it differs
  from pslinkd's last desired route;
- decode or act on headset button, volume, or microphone fields;
- execute arbitrary event hooks;
- expose a stable event API beyond its structured logs;
- manage several adapters concurrently; or
- enable or configure the host audio stack on the user's behalf.
