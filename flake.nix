{
  description = "Katl development shell";

  inputs = {
    nixpkgs.url = "nixpkgs";
  };

  outputs =
    { nixpkgs, ... }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      pkgsFor = system: import nixpkgs { inherit system; };
      shellFor =
        pkgs:
        pkgs.mkShell {
          packages = with pkgs; [
            bashInteractive
            cacert
            cpio
            cryptsetup
            curl
            dnf5
            dosfstools
            e2fsprogs
            erofs-utils
            git
            go
            jq
            mkosi
            openssl
            protobuf
            protoc-gen-go
            qemu_kvm
            rpm
            squashfsTools
            systemd
            util-linux
            xz
            zstd
          ];
        };
    in
    {
      devShells = forAllSystems (
        system:
        let
          pkgs = pkgsFor system;
        in
        {
          default = shellFor pkgs;
          vm = shellFor pkgs;
        }
      );
    };
}
