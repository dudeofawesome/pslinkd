{
  home-manager,
  lib,
  module,
  pkgs,
  pslinkd,
}:

let
  base = {
    home.username = "pslinkd-test";
    home.homeDirectory = "/home/pslinkd-test";
    home.stateVersion = "25.05";

    services.pslinkd = {
      enable = true;
      package = pslinkd;
      audio = {
        fallbackSink = "alsa_output.pci-0000_03_00.1.hdmi-stereo";
      };
    };
  };

  evaluate =
    extra:
    home-manager.lib.homeManagerConfiguration {
      inherit pkgs;
      modules = [
        module
        base
        extra
      ];
    };

  defaults = evaluate { };

  valid = evaluate {
    systemd.user.startServices = "suggest";

    services.pslinkd = {
      audio = {
        headsetSink = "alsa_output.usb-Sony_PlayStation_Link-00.analog-stereo";
        headsetSource = "alsa_input.usb-Sony_PlayStation_Link-00.mono-fallback";
        fallbackSource = "alsa_input.pci-0000_00_1f.3.analog-stereo";
      };
      polling = {
        interval = "0.2s";
        disconnectFailures = 4;
      };
      controls.enable = true;
      controls.volumeMode = "synchronized";
      logLevel = "debug";
    };
  };

  missingSink = home-manager.lib.homeManagerConfiguration {
    inherit pkgs;
    modules = [
      module
      {
        home.username = "pslinkd-test";
        home.homeDirectory = "/home/pslinkd-test";
        home.stateVersion = "25.05";
        services.pslinkd = {
          enable = true;
          package = pslinkd;
        };
      }
    ];
  };

  invalidSourcePair = evaluate {
    services.pslinkd.audio.headsetSource = "headset-source";
  };

  automaticSource = evaluate {
    services.pslinkd.audio.fallbackSource = "fallback-source";
  };

  invalidTiming = evaluate {
    services.pslinkd.polling.interval = "49ms";
  };

  invalidFailures = evaluate {
    services.pslinkd.polling.disconnectFailures = 0;
  };

  invalidControls = evaluate {
    services.pslinkd.controls.enable = "yes";
  };

  invalidVolumeMode = evaluate {
    services.pslinkd.controls.volumeMode = "bypass";
  };

  forcesSuccessfully =
    configuration: (builtins.tryEval configuration.activationPackage.drvPath).success;

  unit = valid.config.systemd.user.services.pslinkd;
  execStart = unit.Service.ExecStart;
  restartTriggers = unit.Unit.X-Restart-Triggers;
  generatedConfig = builtins.elemAt restartTriggers 0;
  automaticSourceConfig = builtins.elemAt automaticSource.config.systemd.user.services.pslinkd.Unit.X-Restart-Triggers 0;
  renderedUnit = valid.config.xdg.configFile."systemd/user/pslinkd.service".source;

  inspection = {
    inherit execStart generatedConfig renderedUnit;
    packageInstalled = lib.elem pslinkd valid.config.home.packages;
    packageRestartTrigger = lib.elem pslinkd restartTriggers;
    wantsWirePlumber = lib.elem "wireplumber.service" unit.Unit.Wants;
    wantedByDefaultTarget = lib.elem "default.target" unit.Install.WantedBy;
    noNewPrivileges = unit.Service.NoNewPrivileges;
    privateDevicesAbsent = !(unit.Service ? PrivateDevices);
    noSystemService = !(valid.config.systemd ? services);
    noLingering = !(valid.config ? users);
    noUdevConfiguration = !(valid.config.services ? udev);
    startServicesPreserved = !valid.config.systemd.user.startServices;
    controlsDefaultDisabled = !defaults.config.services.pslinkd.controls.enable;
    volumeModeDefaultHostOnly = defaults.config.services.pslinkd.controls.volumeMode == "host-only";
  };

  inspectionFile = pkgs.writeText "pslinkd-home-manager-inspection.json" (builtins.toJSON inspection);
in
assert forcesSuccessfully valid;
assert forcesSuccessfully defaults;
assert forcesSuccessfully automaticSource;
assert !(forcesSuccessfully missingSink);
assert !(forcesSuccessfully invalidSourcePair);
assert !(forcesSuccessfully invalidTiming);
assert !(forcesSuccessfully invalidFailures);
assert !(forcesSuccessfully invalidControls);
assert !(forcesSuccessfully invalidVolumeMode);
assert inspection.packageInstalled;
assert inspection.packageRestartTrigger;
assert inspection.wantsWirePlumber;
assert inspection.wantedByDefaultTarget;
assert inspection.noNewPrivileges;
assert inspection.privateDevicesAbsent;
assert inspection.noSystemService;
assert inspection.noLingering;
assert inspection.noUdevConfiguration;
assert inspection.startServicesPreserved;
assert inspection.controlsDefaultDisabled;
assert inspection.volumeModeDefaultHostOnly;
pkgs.runCommand "pslinkd-home-manager-check" { } ''
  grep -F 'run --config ${generatedConfig}' ${inspectionFile}
  grep -F '[Unit]' ${renderedUnit}
  grep -F 'Wants=wireplumber.service' ${renderedUnit}
  grep -F 'After=wireplumber.service' ${renderedUnit}
  grep -F 'X-Restart-Triggers=${generatedConfig}' ${renderedUnit}
  grep -F 'X-Restart-Triggers=${pslinkd}' ${renderedUnit}
  grep -F '[Service]' ${renderedUnit}
  grep -F 'ExecStart=${lib.getExe pslinkd} run --config ${generatedConfig}' ${renderedUnit}
  grep -F 'Restart=on-failure' ${renderedUnit}
  grep -F 'NoNewPrivileges=true' ${renderedUnit}
  grep -F '[Install]' ${renderedUnit}
  grep -F 'WantedBy=default.target' ${renderedUnit}
  grep -F 'headset_sink: alsa_output.usb-Sony_PlayStation_Link-00.analog-stereo' ${generatedConfig}
  grep -F 'fallback_sink: alsa_output.pci-0000_03_00.1.hdmi-stereo' ${generatedConfig}
  grep -F 'headset_source: alsa_input.usb-Sony_PlayStation_Link-00.mono-fallback' ${generatedConfig}
  grep -F 'fallback_source: alsa_input.pci-0000_00_1f.3.analog-stereo' ${generatedConfig}
  grep -F 'interval: 0.2s' ${generatedConfig}
  grep -F 'disconnect_failures: 4' ${generatedConfig}
  grep -F 'enabled: true' ${generatedConfig}
  grep -F 'volume_mode: synchronized' ${generatedConfig}
  grep -F 'level: debug' ${generatedConfig}
  grep -F 'fallback_sink: alsa_output.pci-0000_03_00.1.hdmi-stereo' ${automaticSourceConfig}
  grep -F 'fallback_source: fallback-source' ${automaticSourceConfig}
  grep -F 'enabled: false' ${automaticSourceConfig}
  grep -F 'volume_mode: host-only' ${automaticSourceConfig}
  ! grep -F 'headset_sink:' ${automaticSourceConfig}
  ! grep -F 'headset_source:' ${automaticSourceConfig}
  touch "$out"
''
