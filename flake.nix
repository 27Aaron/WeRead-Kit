{
  description = "weread-kit - WeRead account management and reading challenge automation";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = {
    self,
    nixpkgs,
  }: let
    systems = [
      "x86_64-linux"
      "aarch64-linux"
      "aarch64-darwin"
    ];

    forAllSystems = nixpkgs.lib.genAttrs systems;
  in {
    packages = forAllSystems (
      system: let
        pkgs = import nixpkgs {
          inherit system;
        };
      in rec {
        weread-kit = pkgs.callPackage ./nix/weread-kit.nix {};
        default = weread-kit;
      }
    );

    apps = forAllSystems (system: rec {
      weread-kit = {
        type = "app";
        program = "${self.packages.${system}.weread-kit}/bin/weread-kit";
      };
      default = weread-kit;
    });

    devShells = forAllSystems (
      system: let
        pkgs = import nixpkgs {
          inherit system;
        };
      in {
        default = pkgs.mkShell {
          packages = with pkgs; [
            # Go backend
            go
            gopls
            golangci-lint

            # Frontend tooling
            nodejs
            pnpm

            # Local development utilities
            sqlite
          ];
        };
      }
    );
  };
}
