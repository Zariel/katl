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
        let
          dnfCompat = pkgs.writeShellScriptBin "dnf" ''
            state_home="''${XDG_STATE_HOME:-}"
            if [ -z "$state_home" ]; then
              state_home="''${TMPDIR:-/tmp}/katl-xdg-state"
            fi
            logdir="$state_home/dnf5"
            mkdir -p "$logdir"
            exec ${pkgs.dnf5}/bin/dnf5 --setopt=logdir="$logdir" "$@"
          '';
        in
        pkgs.mkShell {
          packages = with pkgs; [
            bashInteractive
            cacert
            coreutils
            cpio
            cryptsetup
            curl
            dnf5
            dosfstools
            e2fsprogs
            erofs-utils
            findutils
            gawk
            git
            go
            iproute2
            jq
            kubectl
            libvirt
            virt-manager
            mkosi
            mtools
            openssl
            OVMFFull
            podman
            protobuf
            protoc-gen-go
            qemu_kvm
            rpm
            squashfsTools
            systemd
            util-linux
            xz
            zstd
            dnfCompat
          ];

          shellHook = ''
            export TMPDIR="''${TMPDIR:-/tmp}"
            export KATL_OVMF_CODE="''${KATL_OVMF_CODE:-${pkgs.OVMFFull.fd}/FV/OVMF_CODE.fd}"
            export KATL_OVMF_VARS="''${KATL_OVMF_VARS:-${pkgs.OVMFFull.fd}/FV/OVMF_VARS.fd}"
            export KATL_VMTEST_IMAGE_TOOL="''${KATL_VMTEST_IMAGE_TOOL:-${pkgs.qemu_kvm}/bin/qemu-img}"
            export KATL_VMTEST_VIRSH="''${KATL_VMTEST_VIRSH:-${pkgs.libvirt}/bin/virsh}"
            export KATL_VMTEST_LIBVIRT_URI="''${KATL_VMTEST_LIBVIRT_URI:-qemu:///system}"
            export KATL_VMTEST_LIBVIRT_NETWORK="''${KATL_VMTEST_LIBVIRT_NETWORK:-default}"
            export KATL_VMTEST_LIBVIRT_STORAGE_POOL="''${KATL_VMTEST_LIBVIRT_STORAGE_POOL:-default}"
          '';
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
