{
  lib,
  buildGoModule,
  fetchFromGitHub,
}: let
  hashes = builtins.fromJSON (builtins.readFile ./hashes.json);

  # 构建只需要 Go 源码与模块定义;文档、截图、CI 配置等一律排除,
  # 避免无关资源变动导致源码目录与构建产物变化。
  root = fetchFromGitHub {
    owner = "27Aaron";
    repo = "WeRead-Kit";
    tag = "v${hashes.version}";
    hash = hashes.srcHash;
  };
in
  buildGoModule {
    pname = "weread-kit";
    version = hashes.version;

    src = lib.cleanSourceWith {
      src = root;
      filter =
        path: type: let
          relPath = lib.removePrefix (toString root) path;
        in
          relPath == ""
          || builtins.match "/(cmd|internal)(/.*)?" relPath != null
          || builtins.match "/go\\.(mod|sum)" relPath != null
          || relPath == "/LICENSE";
    };

    env.CGO_ENABLED = "0";

    vendorHash = hashes.vendorHash;

    ldflags = [
      "-s"
      "-w"
    ];

    meta = with lib; {
      description = "WeRead account management and reading challenge";
      homepage = "https://github.com/27Aaron/WeRead-Kit";
      license = licenses.mit;
      mainProgram = "weread-kit";
      platforms = platforms.unix;
    };
  }
