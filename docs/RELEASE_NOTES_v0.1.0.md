## ntfy-notifier v0.1.0

首个可用版本：常驻 Windows 托盘，订阅自建 ntfy 的多个 topic，按需用 Lua 处理后弹出 Toast 通知。

### 主要功能

- 多 topic JSON 流订阅（token / basic 认证），断线重连、`since` 续传、消息去重
- 每个 topic 独立 Lua VM：沙箱、200ms 超时、热重载、`setup/on_message`
- 默认映射与脚本自定义标题、正文、优先级、声音、按钮
- 点击 Toast 执行本地动作：`copy` / `open` / `http` / `run`（`run` 默认关闭）
- 敏感复制内容排除 Win+V 历史与云同步
- 托盘菜单、暂停通知、开机自启、日志轮转、单实例
- `--test-toast`、`--test-message`、`--unregister`

### 使用

1. 解压 `ntfy-notifier-v0.1.0-windows-amd64.zip`
2. 复制 `config.example.toml` 为 `config.toml`，填入 ntfy 地址和 token，并把 `app.app_id` 改成自己的唯一 ID
3. 运行 `ntfy-notifier.exe`
4. 用 `ntfy-notifier.exe --test-toast` 验证点击通知复制文本

### 已知限制

- 未签名 exe，首次运行会有 SmartScreen 提示
- 修改 `config.toml` 后需要重启；Lua 脚本支持热重载
- 多 topic 403 降级、Toast `Tag/Group` 替换、安装包暂未实现
- 勿扰 / 专注助手开启时横幅会被系统抑制，通知仍会进入通知中心
- 当前只提供 Windows amd64 构建

校验文件见 `SHA256SUMS`。
