{
  description = "Userspace integration service for PlayStation Link headsets on Linux";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs =
    { nixpkgs, ... }:
    let
      lib = nixpkgs.lib;

      supportedSystems = [
        "aarch64-linux"
        "x86_64-linux"
      ];

      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = import nixpkgs { inherit system; };
          pslinkd = import ./packages/pslinkd/package.nix {
            inherit (nixpkgs) lib;
            inherit pkgs;
          };
        in
        {
          inherit pslinkd;
          default = pslinkd;
        }
      );

      nixosModules.default = {
        imports = [
          ./modules/pslinkd.nix
        ];
      };
    };
}
