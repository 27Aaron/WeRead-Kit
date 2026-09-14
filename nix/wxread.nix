{
  lib,
  buildGoModule,
  fetchFromGitHub,
}: let
  hashes = builtins.fromJSON (builtins.readFile ./hashes.json);
in
  buildGoModule {
    pname = "wxread";
    version = hashes.version;

    src = fetchFromGitHub {
      owner = "27Aaron";
      repo = "wxread";
      tag = "v${hashes.version}";
      hash = hashes.srcHash;
    };

    env.CGO_ENABLED = "0";

    vendorHash = hashes.vendorHash;

    ldflags = [
      "-s"
      "-w"
    ];

    meta = with lib; {
      description = "WeRead account management and reading challenge";
      homepage = "https://github.com/27Aaron/wxread";
      license = licenses.mit;
      mainProgram = "wxread";
      platforms = platforms.unix;
    };
  }
