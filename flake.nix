{
  description = "wxread - WeRead account management and reading challenge automation";

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
        wxread = pkgs.callPackage ./nix/wxread.nix {};
        default = wxread;
      }
    );

    apps = forAllSystems (system: rec {
      wxread = {
        type = "app";
        program = "${self.packages.${system}.wxread}/bin/wxread";
      };
      default = wxread;
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
