# ntfy-notifier

订阅自建 ntfy 的多个 topic，按需用 Lua 处理后，以 Windows Toast 提醒。

## 状态

M0 / M0.5 已在 Windows 真机验证（Toast、scheme、剪贴板、清理），M1 / M2 主体完成：

- ntfy JSON 流订阅、token/basic 认证、重连、`since` 续传、去重
- 每 topic 一个 Lua VM、沙箱、200ms 超时、热重载
- 默认映射与脚本自定义映射
- Windows Toast（`winrt-toast`，纯 Go COM）
- 点击动作：自定义 URI scheme + pending 文件 + 短命实例（copy/open/http/run）
- 托盘、暂停、开机自启、日志轮转

## 开发

```bash
go test ./...
go build ./cmd/ntfy-notifier

# 交叉编译 Windows 版本
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -trimpath -ldflags "-s -w -H=windowsgui" \
  -o dist/ntfy-notifier.exe ./cmd/ntfy-notifier
```

WSL 里可以直接运行 `/mnt/c/.../ntfy-notifier.exe` 验证 Windows Toast。

## 使用

1. 复制 `config.example.toml` 为 `config.toml`，填入自建 ntfy 地址和 token，并把 `app.app_id` 改成自己的唯一 ID（例如 `com.yourname.ntfynotifier`）。
2. 按需修改 `scripts/<topic>.lua`，文件名必须与 topic 一致。
3. 运行 exe，托盘会出现图标；首次启动会注册 `ntfy-notifier://` 协议。
4. 验证通知：`ntfy-notifier.exe --test-toast`。
5. 卸载注册项：`ntfy-notifier.exe --unregister`。

## CLI

| 参数 | 说明 |
| --- | --- |
| `--config <path>` | 配置文件路径，默认 exe 同目录 `config.toml` |
| `--console` | 同时输出日志到 stderr |
| `--debug` | 调试日志 |
| `--test-toast` | 发一条测试 Toast 后退出 |
| `--test-message <json>` | 把一条消息喂进完整管线后退出 |
| `--unregister` | 删除 URI scheme 和开机自启项 |
| `--version` | 版本号 |

## 配置要点

- 脚本热重载；`config.toml` 修改后需要重启程序。
- 首次启动不回放历史；之后按 `lastSeen` 续传。
- 断线补发只通知 `max_message_age`（默认 `5m`）以内的消息；更早的只推进游标、不弹 Toast。设为 `0` 关闭该过滤。
- 重启后不会重复上一条通知：最后一条消息的 id 记在 `state.json`，启动时预热去重表。
- `max_per_min` 在 `[[topic]]` 下可覆盖全局；设为 `0` 表示不限流（验证码 topic 建议如此）。
- 脚本返回 `nil` 表示吞掉通知；返回 table 即通知描述，字段见 `docs/PLAN_Init.md` §8.4。
- 点击动作的 payload 落在 `%LOCALAPPDATA%\ntfy-notifier\pending`，TTL 默认 10 分钟。
- `run` 动作默认关闭，需要显式 `allow_run = true`。

## 目录

- `%APPDATA%\ntfy-notifier\state.json`：lastSeen 与脚本 KV
- `%APPDATA%\ntfy-notifier\logs\app.log`：日志
- `%LOCALAPPDATA%\ntfy-notifier\pending\`：点击动作载荷
