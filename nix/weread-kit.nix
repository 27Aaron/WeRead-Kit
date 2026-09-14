{
  lib,
  buildGoModule,
  fetchFromGitHub,
  runCommand,
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

    # 用 runCommand 剪枝而不是 cleanSourceWith:后者会对 fetch 产物的
    # store 路径做求值期校验,在全新 store 里因 .drv 尚未实现而报
    # "path ... is not valid"(v0.1.6 发布时的实际故障)。
    src = runCommand "weread-kit-src" {} ''
      mkdir -p $out
      cp -r ${root}/cmd ${root}/internal $out/
      install -m 0644 ${root}/go.mod ${root}/go.sum ${root}/LICENSE $out/
    '';

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
