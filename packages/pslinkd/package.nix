{ lib, pkgs }:

pkgs.buildGoModule {
  pname = "pslinkd";
  version = "0.1.0";

  src = ../..;
  vendorHash = "sha256-Gz2RQZkR2uEeoVM6OmsS9AAqQnqkp+TD/vFGJiQ4nmc=";

  nativeBuildInputs = with pkgs; [
    makeWrapper
    pkg-config
  ];

  buildInputs = [ pkgs.systemd ];

  env.CGO_ENABLED = "1";

  postInstall = ''
    wrapProgram "$out/bin/pslinkd" \
      --prefix PATH : ${lib.makeBinPath [ pkgs.wireplumber ]}

    install -Dm644 /dev/stdin \
      "$out/lib/udev/rules.d/70-pslinkd.rules" <<'RULE'
    SUBSYSTEM=="hidraw", ATTRS{idVendor}=="054c", ATTRS{idProduct}=="0ecc", GROUP="pslink", MODE="0660"
    RULE
  '';

  meta = {
    description = "Userspace integration service for PlayStation Link headsets";
    homepage = "https://github.com/dudeofawesome/pslinkd";
    license = lib.licenses.mit;
    mainProgram = "pslinkd";
    platforms = lib.platforms.linux;
  };
}
