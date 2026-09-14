# wxread

[![Release](https://img.shields.io/github/v/release/27Aaron/wxread)](https://github.com/27Aaron/wxread/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

## 功能特性

- **账号管理**:微信扫码添加账号;凭据存储于本地 SQLite,自动轮换续期
- **阅读挑战**:以 30 秒心跳模拟真实阅读上报;支持每日定时调度、断点续跑、指定书单
- **推送通知**:支持 Bark、Telegram、Server酱、PushPlus;
- **Web UI**:暖纸色主题,深浅色自适应,窄屏响应式布局;运行日志自动刷新
- **登录鉴权**:可选的账号密码保护,签名 cookie 7 天有效,带 CSRF 防护

## 快速开始

### docker run

```bash
docker run -d --name wxread \
  -p 8080:8080 \
  --stop-signal SIGINT \
  -e WXREAD_HOST=0.0.0.0 \
  -v wxread-data:/data \
  ghcr.io/27aaron/wxread:latest
```

> `--stop-signal SIGINT` 让容器停止时走应用的优雅退出路径;不配也能用,但停止会退化成等超时后强杀。

### 下载二进制

从 [Releases](https://github.com/27Aaron/wxread/releases) 下载对应平台的二进制文件

```bash
chmod +x wxread-<平台>
./wxread-<平台>
```

### 源码构建

```bash
go build -o wxread ./cmd/wxread
./wxread
```

需要 Go 1.26+,或使用仓库自带的 Nix 开发环境:`nix develop`。

### Nix

```bash
nix run github:27Aaron/wxread              # 直接运行
nix profile install github:27Aaron/wxread  # 安装到 profile
```

也可以作为 flake input 引入你自己的 flake:

```nix
{
  inputs.wxread.url = "github:27Aaron/wxread";
}
```

随后通过 `wxread.packages.<system>.default` 引用。

## 使用教程

启动后浏览器打开 `http://127.0.0.1:8080`。若配置了 `WXREAD_USERNAME` / `WXREAD_PASSWORD`,会先跳转到登录页(登录会话 7 天有效,服务重启不失效);未配置则直接进入。

### 1. 添加账号

进入「我的账号」→ 点击「添加账户」→ 生成二维码后**打开微信扫一扫**,并在手机上确认登录。登录成功后账号出现在列表中,凭据自动保存在本地。

### 2. 配置阅读挑战

进入「阅读挑战」页:

1. **选择书籍**(必选):至少勾选 1 本;选 1 本固定阅读,选多本时随机阅读其中一本
2. 点击「**保存配置**」,不选书无法保存
3. 按需调整「每天自动参与阅读挑战」开关、开始时间(默认 03:00)和每天时长(默认 30 分钟)

### 3. 立即执行 / 停止

点击「**立即执行**」立刻开始一场阅读会话(不打扰每日调度);会话进行中按钮会变成红色的「**停止阅读**」,点击即可终止。已上报的时长不会回滚。

> 会话每 30 秒记 0.5 分钟,页面会实时显示进度(如「阅读中 6.5/30 分钟」)。
>
> 断点续跑规则:**用户主动停止 = 当日视为结束**,再点「立即执行」会开一场全新的会话;**进程重启 / 异常退出 = 下次启动自动续跑**当日未完成的会话。

### 4. 查看运行日志

「运行日志」页每 10 秒自动刷新,可按账号、级别过滤。心跳上报失败、凭据轮换、调度器跳过等事件都会记录在这里。

### 5. 推送通知(可选)

「推送设置」页配置 Bark / Telegram / Server酱 / PushPlus 渠道并可发送测试消息。阅读会话完成、中断、失败时会自动推送结果。

### 6. 凭据过期怎么办

日志出现「会话已过期」时,程序会自动尝试续期;若频繁失败,到「我的账号」对该账号「刷新数据」,仍不行就删除账号重新扫码登录。

## 数据与备份

所有状态都存在一个 SQLite 文件里(默认 `data/wxread.db`,容器部署为 `./data/`):

- 包含:账号凭据、阅读配置、运行日志、Web 会话签名密钥
- 数据库文件自动收紧权限为 `0600`
- 备份 = 备份该目录;应用运行时持续写入,定期备份前建议先停服务

## 配置

复制 `.env.example` 为 `.env`,或直接设置环境变量:

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `WXREAD_HOST` | `127.0.0.1` | 监听地址(容器内需设为 `0.0.0.0`) |
| `WXREAD_PORT` | `8080` | 监听端口 |
| `WXREAD_DB` | `data/wxread.db` | SQLite 数据库路径 |
| `WXREAD_USERNAME` | 空 | Web 登录账号,与密码**同时配置**才启用登录鉴权 |
| `WXREAD_PASSWORD` | 空 | Web 登录密码 |

启用鉴权后,未登录访问页面会跳转登录页、访问 API 返回 401。

## 开发

```bash
nix develop          # 进入开发环境(go / golangci-lint / nodejs 等)
go build ./...       # 构建
go test ./...        # 测试
gofmt -l .           # 格式检查
```

## 免责声明

本项目仅供个人学习与研究,请勿用于商业用途。使用本项目产生的任何问题由使用者自行承担,请尊重微信读书的用户协议,合理控制使用强度。

## 参考

- [findmover/wxread](https://github.com/findmover/wxread)
