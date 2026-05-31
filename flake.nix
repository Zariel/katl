{
  description = "Katl development shell";

  inputs = {
    nixpkgs.url = "nixpkgs";
  };

  outputs = { nixpkgs, ... }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
      ];

      forAllSystems = nixpkgs.lib.genAttrs systems;

      pkgsFor = system: import nixpkgs { inherit system; };
    in
    {
      devShells = forAllSystems (system:
        let
          pkgs = pkgsFor system;

          mkosiBuildPackages = with pkgs; [
            bashInteractive
            cacert
            coreutils
            cpio
            curl
            dnf5
            dosfstools
            e2fsprogs
            gnutar
            keyutils
            kmod
            mkosi
            mtools
            opensc
            openssl
            rpm
            systemd
            systemdUkify
            xfsprogs
            zstd
          ];
        in
        {
          default = pkgs.mkShell {
            packages = mkosiBuildPackages;

            SSL_CERT_FILE = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt";
            NIX_SSL_CERT_FILE = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt";
          };

          vm = pkgs.mkShell {
            packages = mkosiBuildPackages ++ [
              pkgs.OVMF
              pkgs.qemu_kvm
            ];

            SSL_CERT_FILE = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt";
            NIX_SSL_CERT_FILE = "${pkgs.cacert}/etc/ssl/certs/ca-bundle.crt";
          };
        });
    };
}
