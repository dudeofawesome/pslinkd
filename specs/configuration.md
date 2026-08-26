# Command line and configuration

## Command line

V1 exposes one command:

```text
pslinkd run [--config PATH]
```

When `--config` is omitted, the path is
`$XDG_CONFIG_HOME/pslinkd/config.yaml`. If `XDG_CONFIG_HOME` is unset, the
implementation uses Go's standard user configuration directory resolution
(normally `$HOME/.config/pslinkd/config.yaml` on Linux).

Unknown flags or positional arguments are errors. `run` validates the complete
configuration before performing device or audio I/O. V1 has no configuration
reload; changes require a process restart.

## YAML document

The file is strict YAML. Comments are supported. Unknown keys, duplicate keys,
wrong scalar types, invalid duration strings, and missing required values MUST
fail validation. YAML implicit coercions MUST NOT turn strings such as node
names into other types.

Canonical example:

```yaml
audio:
  # Omit headset_sink to discover the selected adapter's sink automatically.
  # headset_sink: alsa_output.usb-Sony_Interactive_Entertainment_PlayStation_Link_Adapter_SERIAL-00.analog-stereo
  fallback_sink: alsa_output.pci-0000_03_00.1.hdmi-surround-extra3

  # fallback_source enables source routing. Omit headset_source to discover the
  # selected adapter's source automatically, or set an exact-name override.
  # headset_source: alsa_input.usb-Sony_Interactive_Entertainment_PlayStation_Link_Adapter_SERIAL-00.mono-fallback
  # fallback_source: alsa_input.pci-0000_00_1f.3.analog-stereo

polling:
  interval: 200ms
  disconnect_failures: 3

logging:
  level: info
```

## Schema

| Key | Type | Default | Validation |
| --- | --- | --- | --- |
| `audio.headset_sink` | string | unset | nonempty exact `node.name` override; unset discovers automatically |
| `audio.fallback_sink` | string | required | nonempty exact `node.name` |
| `audio.headset_source` | string | unset | nonempty exact `node.name` override; requires fallback source |
| `audio.fallback_source` | string | unset | nonempty; enables source routing and automatic headset-source discovery |
| `polling.interval` | duration string | `200ms` | `50ms..10s` |
| `polling.disconnect_failures` | integer | `3` | `1..50` |
| `logging.level` | string enum | `info` | `debug`, `info`, `warn`, or `error` |

`fallback_source` controls whether default-source routing is enabled. With it
set, an omitted `headset_source` is automatically discovered; with it unset,
source routing is disabled and `headset_source` MUST also be unset. Button,
volume, and microphone interaction settings are reserved for v1.1 and are
unknown-key errors in a v1 configuration.

Device IDs, report layout, routing retry policy, and log format are not
configurable in v1.

## Logs

`run` writes one JSON object per line for journald. Every record contains at
least `time`, `level`, `event`, and `message`. Relevant records include stable
fields such as `device_path`, `adapter_present`, `headset_connected`, `attempt`,
`target_name`, `target_kind`, `candidate_names`, `candidate_priorities`, and
`error` when applicable.

Event names MUST distinguish at least:

- daemon start/stop;
- adapter added/removed and multiple-adapter warning;
- headset connected/disconnected;
- automatic audio-target selection with multiple eligible nodes;
- audio action succeeded/retrying; and
- HID/discovery recovery or fatal failure.

The JSON log schema is operational observability, not a versioned IPC API.
Secrets are not part of configuration and raw 64-byte reports MUST only be
logged at debug level.
