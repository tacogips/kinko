{

  description = "A Golang project";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:
    let
      # Single source of truth for version
      version = builtins.replaceStrings [ "\n" ] [ "" ] (builtins.readFile ./internal/build/VERSION);
    in
    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
        };
      in
      {
        packages = {
          kinko = pkgs.buildGoModule {
            pname = "kinko";
            inherit version;
            src = ./.;
            vendorHash = "sha256-ovuuT/XMQrHUcs9DLDT8Bmsyfgg9FokjX6Ga9E8JKGE=";
            subPackages = [ "cmd/kinko" ];
            ldflags = [
              "-s"
              "-w"
              "-X githus.com/tacogips/kinko/internal/build.version=${version}"
            ];
            meta = with pkgs.lib; {
              description = "A Golang project";
              homepage = "https://github.com/user/repo";
              license = licenses.mit;
              maintainers = [ ];
            };
          };

          default = self.packages.${system}.kinko;
        };

        apps = {
          kinko = {
            type = "app";
            program = "${self.packages.${system}.kinko}/bin/kinko";
          };

          default = self.apps.${system}.kinko;
        };

        devShells.default = pkgs.mkShell {
          nativeBuildInputs = with pkgs; [
            direnv
            go
            gopls
            gotools
            golangci-lint
            go-task
            bashInteractive
            zsh
            fish
          ];

          shellHook = ''
            export GOPATH="$HOME/.cache/go/githus.com/tacogips/kinko"
            export GOMODCACHE="$HOME/.cache/go/mod"
            mkdir -p "$GOPATH" "$GOMODCACHE"
            echo "Go development environment ready"
            echo "GOPATH: $GOPATH"
            echo "GOMODCACHE: $GOMODCACHE"
            echo "Go version: $(go version)"
            echo "Task version: $(task --version)"
            echo "golangci-lint version: $(golangci-lint --version)"
          '';
        };
      }
    );
}
