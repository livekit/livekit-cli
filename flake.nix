{
  description = "Command line interface to LiveKit";

  inputs = {
    nixpkgs.url = "nixpkgs/nixos-unstable";
    utils.url = "github:numtide/flake-utils";

    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs = { self, nixpkgs, utils, gomod2nix }:
    utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [ gomod2nix.overlays.default ];
        };
      in {
        packages.default = pkgs.buildGoApplication {
          pname = "livekit-cli";
          version = "1.3.4";
          src = ./.;
          subPackages = "cmd/livekit-cli";
          modules = ./gomod2nix.toml;
        };
        devShells.default = with pkgs; mkShell {
          buildInputs = [
            go
            gopls
            gomod2nix.packages.${system}.default
          ];
        };
      }
    );
}
