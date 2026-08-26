{
  description = "Userspace integration service for PlayStation Link headsets on Linux";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

    home-manager = {
      url = "github:nix-community/home-manager";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      home-manager,
      nixpkgs,
      ...
    }:
    let
      supportedSystems = [
        "aarch64-linux"
        "x86_64-linux"
      ];

      formatterSystems = supportedSystems ++ [
        "aarch64-darwin"
      ];

      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
      homeManagerModule = import ./modules/pslinkd.nix;
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
          pslinkd = pkgs.callPackage ./packages/pslinkd/package.nix { };
        in
        {
          inherit pslinkd;
          default = pslinkd;
        }
      );

      homeManagerModules = {
        pslinkd = homeManagerModule;
        default = homeManagerModule;
      };

      formatter = nixpkgs.lib.genAttrs formatterSystems (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
        in
        pkgs.nixfmt-rfc-style
      );

      checks = forAllSystems (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
          pslinkd = pkgs.callPackage ./packages/pslinkd/package.nix { };
        in
        {
          package = pslinkd;

          home-manager = import ./nix/checks/home-manager.nix {
            inherit home-manager pkgs pslinkd;
            inherit (nixpkgs) lib;
            module = homeManagerModule;
          };

          udev-rule = pkgs.runCommand "pslinkd-udev-rule-check" { } ''
            rule=${pslinkd}/lib/udev/rules.d/70-pslinkd.rules
            grep -Fx 'SUBSYSTEM=="hidraw", ATTRS{idVendor}=="054c", ATTRS{idProduct}=="0ecc", GROUP="pslink", MODE="0660"' "$rule"
            test "$(wc -l < "$rule")" -eq 1
            touch "$out"
          '';
        }
      );
    };
}
