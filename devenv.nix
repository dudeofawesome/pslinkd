{ lib, pkgs, ... }:

{
  packages =
    (with pkgs; [
      go_1_25
      gotools
      nix
      nixfmt
      pkg-config
    ])
    ++ lib.optionals pkgs.stdenv.isLinux (
      with pkgs;
      [
        systemd
        wireplumber
      ]
    );

  env.CGO_ENABLED = "1";

  scripts.check.exec = ''
    gofmt -w .
    go test ./...
    go vet ./...
  '';
}
