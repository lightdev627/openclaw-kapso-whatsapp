{
  description = "OpenClaw plugin: Kapso WhatsApp bridge";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils, ... }:
    {
      homeManagerModules.default = import ./nix/module.nix;
    }
    //
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import nixpkgs { inherit system; };

        cli = pkgs.buildGoModule {
          pname = "kapso-whatsapp-cli";
          version = "0.2.0";
          src = ./.;
          subPackages = [ "cmd/kapso-whatsapp-cli" ];
          vendorHash = "sha256-Upjt0Q2G6x5vGf0bG0TS9uWrHBow8/cQsZexhMgVb2I=";
          env.CGO_ENABLED = "0";
        };

        bridge = pkgs.buildGoModule {
          pname = "kapso-whatsapp-bridge";
          version = "0.2.0";
          src = ./.;
          subPackages = [ "cmd/kapso-whatsapp-bridge" ];
          vendorHash = "sha256-Upjt0Q2G6x5vGf0bG0TS9uWrHBow8/cQsZexhMgVb2I=";
          env.CGO_ENABLED = "0";
        };
      in {
        packages = {
          inherit cli bridge;
          default = cli;
        };

        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            go
            gopls
            golangci-lint
            goreleaser
            just
          ];
        };

        openclawPlugin = {
          name = "kapso-whatsapp";
          skills = [ ./skills/whatsapp ];
          packages = [ cli ];
          needs = {
            stateDirs = [ ".config/kapso-whatsapp" ];
            requiredEnv = [ "KAPSO_API_KEY" "KAPSO_PHONE_NUMBER_ID" ];
          };
        };
      }
    );
}
