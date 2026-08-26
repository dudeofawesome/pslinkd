{
  config,
  lib,
  pkgs,
  ...
}:

let
  inherit (lib)
    mkEnableOption
    mkIf
    mkOption
    types
    ;

  cfg = config.services.pslinkd;
  yaml = pkgs.formats.yaml { };

  nonEmptyString = types.addCheck types.str (value: lib.trim value != "");

  durationFactors = {
    ns = 1;
    us = 1000;
    "µs" = 1000;
    "μs" = 1000;
    ms = 1000000;
    s = 1000000000;
    m = 60000000000;
    h = 3600000000000;
  };

  parseDuration =
    value:
    let
      parseComponents =
        remaining:
        let
          match = builtins.match "^([0-9]+|[0-9]*\\.[0-9]+)(ns|us|µs|μs|ms|s|m|h)(.*)$" remaining;
        in
        if match == null then
          null
        else
          let
            numberText = builtins.elemAt match 0;
            unit = builtins.elemAt match 1;
            rest = builtins.elemAt match 2;
            number = builtins.fromJSON (if lib.hasPrefix "." numberText then "0${numberText}" else numberText);
            tail = if rest == "" then 0 else parseComponents rest;
          in
          if tail == null then null else (number * durationFactors.${unit}) + tail;
    in
    if value == "0" then 0 else parseComponents (lib.removePrefix "+" value);

  intervalNanoseconds = parseDuration cfg.polling.interval;
  intervalIsValid =
    intervalNanoseconds != null
    && intervalNanoseconds >= 50000000
    && intervalNanoseconds <= 10000000000;

  configFile = yaml.generate "pslinkd-config.yaml" {
    audio = {
      headset_sink = cfg.audio.headsetSink;
      fallback_sink = cfg.audio.fallbackSink;
    }
    // lib.optionalAttrs (cfg.audio.headsetSource != null) {
      headset_source = cfg.audio.headsetSource;
      fallback_source = cfg.audio.fallbackSource;
    };

    polling = {
      interval = cfg.polling.interval;
      disconnect_failures = cfg.polling.disconnectFailures;
    };

    logging.level = cfg.logLevel;
  };
in
{
  options.services.pslinkd = {
    enable = mkEnableOption ''
      pslinkd. A running PipeWire/WirePlumber user session with wpctl
      compatibility is required. Hidraw authorization through the host pslink
      group and the package's udev rule must be configured by an administrator
    '';

    package = mkOption {
      type = types.package;
      default = pkgs.callPackage ../packages/pslinkd/package.nix { };
      defaultText = lib.literalExpression "pkgs.pslinkd";
      description = "The pslinkd package to install and run.";
    };

    audio = {
      headsetSink = mkOption {
        type = nonEmptyString;
        description = "Exact WirePlumber node.name for the headset sink.";
      };

      fallbackSink = mkOption {
        type = nonEmptyString;
        description = "Exact WirePlumber node.name for the fallback sink.";
      };

      headsetSource = mkOption {
        type = types.nullOr nonEmptyString;
        default = null;
        description = "Exact headset source node.name, when source routing is enabled.";
      };

      fallbackSource = mkOption {
        type = types.nullOr nonEmptyString;
        default = null;
        description = "Exact fallback source node.name, when source routing is enabled.";
      };
    };

    polling = {
      interval = mkOption {
        type = types.str;
        default = "200ms";
        description = "Feature-report polling interval, from 50ms through 10s.";
      };

      disconnectFailures = mkOption {
        type = types.ints.between 1 50;
        default = 3;
        description = "Consecutive unsuccessful samples required to disconnect.";
      };
    };

    logLevel = mkOption {
      type = types.enum [
        "debug"
        "info"
        "warn"
        "error"
      ];
      default = "info";
      description = "JSON log threshold.";
    };
  };

  config = mkIf cfg.enable {
    assertions = [
      {
        assertion = pkgs.stdenv.hostPlatform.isLinux;
        message = "services.pslinkd is supported only on Linux";
      }
      {
        assertion = (cfg.audio.headsetSource == null) == (cfg.audio.fallbackSource == null);
        message = "services.pslinkd audio sources must be configured as a pair";
      }
      {
        assertion = intervalIsValid;
        message = "services.pslinkd.polling.interval must be a duration from 50ms through 10s";
      }
    ];

    home.packages = [ cfg.package ];

    systemd.user.services.pslinkd = {
      Unit = {
        Description = "PlayStation Link headset integration";
        Wants = [ "wireplumber.service" ];
        After = [ "wireplumber.service" ];
        X-Restart-Triggers = [
          configFile
          cfg.package
        ];
      };

      Service = {
        ExecStart = "${lib.getExe cfg.package} run --config ${configFile}";
        Restart = "on-failure";
        RestartSec = "5s";
        KillSignal = "SIGTERM";
        TimeoutStopSec = "15s";
        NoNewPrivileges = true;
      };

      Install.WantedBy = [ "default.target" ];
    };
  };
}
