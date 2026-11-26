# ntfy-notifier 初始计划

- 状态：草案 v0.1
- 日期：2026-11-25
- 目标平台：Windows 10/11（amd64 优先，arm64 顺带支持）
- 开发环境：WSL2（Linux）交叉编译，真机验证 Toast 与点击激活

## 0. 实施进度（2026-11-26）

- M0 / M0.5 已实现并在 Windows 真机验证：AUMID 注册、Toast 发送、URI scheme 注册、模拟激活后剪贴板写入、`--unregister` 清理。
- M1 基本完成：多 topic、流式订阅、重连、`since` 续传、去重、state、托盘常驻、暂停。
- M2 基本完成：Lua 沙箱、超时、热重载、`on_click` / `buttons`、动作执行器（copy/open/http/run）。
- 未完成：`config.toml` 热重载、多 topic 403 降级、Toast `Tag/Group`、安装包、CI。
- 待真机确认：点击真实 Toast 时 protocol 激活是否按预期触发（用 `--test-toast` 验证）。

## 1. 项目目标

做一个常驻托盘的 Windows 小工具：

1. 订阅自建 ntfy 上的多个 topic（token 认证，不走代理）。
2. 每条消息默认直通为 Windows Toast：标题、正文、优先级、声音、点击链接。
3. 如果存在与 topic 同名的 Lua 脚本，则先交给脚本处理，由脚本决定是否弹出、以及 Toast 的完整形态，包括点击后执行的动作。
4. 脚本热加载，放入 `scripts/<topic>.lua` 即自动挂载。
5. 单 exe、无需管理员权限、支持开机自启。

典型场景：

- 短信验证码 topic：消息体是 JSON，脚本拼出「验证码 123456 / 示例公司 · 发送方 · 时间」，点击通知收起并把验证码复制到剪贴板。
- coding agent topic：低频短通知，提醒用户去处理，不承载长输出。

## 2. 非目标（v1 明确不做）

- 不做 Windows 服务（Session 0 无法弹通知）。
- 不做进程内 Toast 点击回调（不上 COM 激活服务器，见 §6 的备选方案）。
- 不做内联回复、输入框、进度条。
- 不做图形化配置界面，TOML 手改 + 托盘重载。
- 不做消息历史同步/多端已读。
- 不渲染 Markdown / 代码高亮。
- 不主动管理勿扰模式（专注助手），只在文档里说明限制。

## 3. 技术选型

| 关注点 | 选型 | 说明 |
| --- | --- | --- |
| 语言 | Go 1.25.12+ | `winrt-toast` 的硬性要求 |
| ntfy 订阅 | 标准库 `net/http` + `bufio.Scanner` 读 NDJSON | 零额外依赖 |
| Lua | `github.com/yuin/gopher-lua` | 纯 Go，Lua 5.1，`SetContext` 可做超时 |
| Toast | `github.com/mtunjic/winrt-toast` v1.2.x | 纯 Go 直调 COM，无 PowerShell，支持 protocol 激活 |
| 托盘 | `fyne.io/systray` | Windows 上纯 Go，无 cgo |
| 配置 | `github.com/BurntSushi/toml` | 带注释、手改友好 |
| 热重载 | `github.com/fsnotify/fsnotify` | 监听 scripts 目录 |
| 日志 | 标准库 `log/slog` + `gopkg.in/natefinch/lumberjack.v2` | 文件轮转 |
| 单实例 | `golang.org/x/sys/windows` + `CreateMutexW` | 命名互斥量 |
| 剪贴板 | 自己封装 user32/kernel32 | 需要额外的剪贴板历史排除格式 |

构建约束：`CGO_ENABLED=0`，产物是单个 `.exe`。

## 4. 总体架构

```
                 ┌──────────── config.toml（可热重载）────────────┐
                 │                                               │
 ntfy ──► subscriber(每 server+token 一组) ──► router ──► lua worker(每 topic) ──► notifier
          - NDJSON 解码                       - event 过滤   - 过滤/改写/声明动作    - 限流/静音
          - since / 重连 / 退避               - (topic,id)去重 - 超时/热重载          - Toast
          - keepalive 看门狗                  - 有界队列      - 动作落盘             - AUMID
                 │                                               │
                 └──────────► state.json（lastSeen / kv）◄────────┘
                                        │
 点击 Toast ──► shell 启动 exe --activate <uri> ──► 读取 pending 动作 ──► 执行 ──► 退出
```

进程只有一个主实例：托盘常驻。`--activate` 是例外，它不初始化托盘、不抢单实例锁，执行完动作立即退出。

## 5. 目录结构

```
cmd/ntfy-notifier/main.go       # flag、--activate 分支、单实例、生命周期
internal/config/                # TOML 解析、校验、默认值、热重载
internal/subscribe/             # 流式/轮询订阅、认证、重连、去重
internal/model/                 # Message / NotificationSpec / Action
internal/router/                # topic 分发、去重、令牌桶、静音时段
internal/luaengine/             # VM 管理、沙箱、host API、自动挂载
internal/notify/                # Notifier 接口、Windows Toast、console 实现
internal/activate/              # scheme 注册、URI 解析、pending 存储
internal/action/                # copy/open/http/run 执行器
internal/store/                 # state.json 原子读写
internal/tray/                  # 托盘菜单与状态
internal/logx/                  # slog、轮转、脱敏、内存环形缓冲
scripts/                        # 示例脚本（sms.lua 等）
assets/                         # 图标资源
docs/                           # 文档
```

`internal/notify` 提供 `notify_other.go`（打印到控制台），保证除 Toast 外的逻辑都能在 WSL 里开发和测试。

## 6. 点击动作链路（核心设计）

### 6.1 为什么需要它

Toast 被点击时，进程默认收不到事件。`winrt-toast` 生成的是：

```xml
<toast activationType="protocol" launch="...">
```

所以整条通知的点击等价于让 shell 打开一个 URI。我们利用这一点，把点击变成一次轻量的「二次启动」。

### 6.2 链路

```
Lua 返回 on_click/buttons
  → 每个动作生成 uuid（crypto/rand，128 bit）
  → 写 %LOCALAPPDATA%\ntfy-notifier\pending\<uuid>.json
  → Toast 的 launch / 按钮 arguments = ntfy-notifier://act?id=<uuid>
  → 用户点击
  → shell 执行：ntfy-notifier.exe --activate "ntfy-notifier://act?id=<uuid>"
  → 校验 id、读文件、校验动作、执行、删除文件、退出
```

要点：

- URI 里只放随机 id，不放正文/验证码，避免长度限制、编码问题和 argv 泄露。
- pending 文件 TTL 10 分钟；启动时清理过期文件；执行后立即删除。
- 主进程是否在运行都能工作，动作是自包含的。
- 未来需要进程内动作（标记已读、暂停订阅）时，再加命名管道 IPC，不改现有协议。

### 6.3 pending 文件格式

```json
{
  "v": 1,
  "created": 1795564800,
  "expires": 1795565400000,
  "action": {
    "type": "copy",
    "text": "123456",
    "sensitive": true
  }
}
```

动作类型：

| type | 字段 | 说明 |
| --- | --- | --- |
| copy | `text`, `sensitive` | 写剪贴板；sensitive 时排除 Win+V 历史和云同步 |
| open | `url` | 仅允许 http/https |
| http | `method`, `url`, `headers`, `body`, `timeout` | host 白名单，默认 5s 超时 |
| run | `cmd`, `args` | 默认关闭，需 `[actions] allow_run = true` |

### 6.4 URI scheme 注册

写 HKCU，不需要管理员：

```
HKCU\Software\Classes\ntfy-notifier
  (Default)        = "URL:ntfy-notifier"
  URL Protocol     = ""
HKCU\Software\Classes\ntfy-notifier\DefaultIcon
  (Default)        = "<exe路径>,0"
HKCU\Software\Classes\ntfy-notifier\shell\open\command
  (Default)        = "\"<exe路径>\" --activate \"%1\""
```

启动时幂等注册（exe 移动后自动修正），并调用 `SHChangeNotify(SHCNE_ASSOCCHANGED, ...)` 通知 shell。`--unregister` 时删除整个键。

### 6.5 剪贴板

- `OpenClipboard` 可能被占用，重试 5 次、间隔 100ms。
- `CF_UNICODETEXT` 用 `GlobalAlloc(GMEM_MOVEABLE)` + `SetClipboardData`，数据所有权交给系统，进程退出后内容仍在。
- `sensitive = true` 时额外写入：
  - `ExcludeClipboardContentFromMonitorProcessing`（空数据）
  - `CanIncludeInClipboardHistory` = DWORD 0
  - `CanUploadToCloudClipboard` = DWORD 0
- 失败写日志，不弹错误框。

### 6.6 备选方案（暂不实现）

如果 protocol 激活在某些 Windows 版本上表现不符合预期（例如通知中心残留、点击不触发），升级为 COM 激活服务器：

- 开始菜单快捷方式携带 AUMID + `ToastActivatorCLSID`；
- 注册 `CLSID\...\LocalServer32`；
- 实现 `INotificationActivationCallback`，`CoRegisterClassObject` 后接收激活。

工作量约多 1–2 天，v1 不作为默认路径。

## 7. ntfy 订阅

### 7.1 连接组织

- 按 `(base_url, token/username)` 分组，一组用逗号拼接 topic，走一条 `/topic1,topic2/json` 长连接。
- 单组最多 150 个 topic（ntfy 服务端限制），超出时报错。
- 组内某个 topic 权限不足导致整条流 403 时，回退成按 topic 单独连接，隔离问题。

### 7.2 认证与网络

- token：`Authorization: Bearer tk_xxx`；也支持 basic（username/password）。
- 自签证书：`[server] tls_ca`，加载进 `RootCAs`；`insecure_skip_verify` 仅调试用。
- 不走代理：`Proxy: nil`（避免被环境变量里的代理接管）；预留 `proxy` 配置项。

### 7.3 重连与续传

- 长连接不设总超时；用 `context` + 自定义 watchdog。
- ntfy 默认 45s 一个 `keepalive`；90s 无任何字节则主动断开重连。
- 退避：1s → 60s，指数 + 抖动；收到 `open` 事件立即重置。
- 401/403：降频到 5 分钟一次，只提示一次（托盘状态 + 一条 Toast），避免被服务端 ban。
- 重连时 `since` 取组内各 topic `lastSeen` 的最小值减 5s（Unix 时间戳）；没有任何 `lastSeen` 的 topic 不参与取最小值，全组都没有则不传 `since`。多 topic 下不用消息 ID，因为 `since=<id>` 的语义是单 topic 的。
- 用 `(topic,id)` LRU 去重（容量 1000，TTL 10 分钟），覆盖重连补发的重复消息。
- 首次启动默认不回放历史；`initial_since` 可配（如 `10m`、`latest`）。

### 7.4 事件处理

| event | 处理 |
| --- | --- |
| `open` | 标记连接成功，记录 topics，重置退避 |
| `message` | 进入 router |
| `keepalive` | 刷新 watchdog，忽略 |
| `message_delete` / `message_clear` | v1 只记日志（未来配合 Toast Tag/Group 做定向删除） |
| `poll_request` | 忽略 |

### 7.5 轮询降级

`mode = "poll"` 时每 30s 请求 `?poll=1&since=<lastSeen>`，处理完关闭连接。用于流式被中间设备缓冲/拦截的环境，仍依赖去重。

### 7.6 消息解析

- `bufio.Scanner` 缓冲区调到 1MB（默认 64KB 不够）。
- JSON 字段：`id/time/event/topic/message/title/priority/tags/click/icon/actions/attachment/sender`。
- `priority` 缺省按 3 处理；`tags` 统一转 `[]string`。
- 非法行记日志后丢弃，不中断连接。

## 8. Lua 引擎

### 8.1 挂载规则

- 默认：`scripts/<topic>.lua`，文件名必须与 topic 完全一致。
- ntfy topic 只允许 `[-_A-Za-z0-9]{1,64}`，不含路径分隔符，所以不需要子目录映射。
- TOML 中可用 `script = "路径"` 覆盖。
- NTFS 大小写不敏感：启动时检测仅大小写不同的 topic/脚本冲突，直接报配置错误。

### 8.2 热重载

- `fsnotify` 监听 scripts 目录，防抖 300ms（编辑器原子保存会触发多次事件）。
- 新增：编译并挂载；修改：编译新 VM 后原子替换；删除：卸载并回落默认映射。
- 编译失败：保留上一版继续工作，日志 + 托盘错误标记。
- 重载会重建 VM 并重跑 `setup()`；`ctx.kv` 持久化数据保留。

### 8.3 执行模型

- 每个 topic 一个 worker goroutine + 一个 `LState`，串行执行（`LState` 不是并发安全的）。
- 每条消息 `SetContext` 设置超时（默认 200ms）。
- 超时视为 VM 已污染：销毁并重建，该条消息走默认映射。
- 运行时错误：记日志，该条消息走默认映射（避免丢消息）。

### 8.4 脚本契约

```lua
-- 可选，加载时执行一次
function setup(ctx)
  ctx.kv.count = ctx.kv.count or 0
end

-- 每条消息调用
-- 返回 nil / false：不通知
-- 返回 table：通知描述
function on_message(m)
  return { title = "...", body = "..." }
end
```

`m` 字段：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | 消息 ID |
| `time` | number | Unix 时间戳 |
| `topic` | string | topic |
| `title` | string | 可能为空 |
| `message` | string | 原始正文 |
| `priority` | number | 1–5 |
| `tags` | table | 字符串数组 |
| `click` | string | 可能为空 |
| `icon` | string | 可能为空 |
| `actions` | table | ntfy 原始 actions 数组 |
| `attachment` | table | name/url/type/size/expires |
| `sender` | string | 可能为空 |
| `raw` | string | 原始 JSON 行 |

返回的通知描述：

| 字段 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `title` | string | `[topic]` | Toast 标题 |
| `body` | string | `m.message` | 正文，支持换行，超过 300 字截断 |
| `priority` | number | `m.priority` | 1–5，映射声音/时长 |
| `sound` | string | 按 priority | `default` / `mail` / `silent` |
| `duration` | string | `short` | `short` / `long` |
| `icon` | string | 全局图标 | 绝对路径 PNG |
| `click` | string | `m.click` | 整条通知打开 URL（http/https） |
| `on_click` | table | 无 | 整条通知执行动作；与 `click` 同时存在时优先生效，`click` 自动降级为一个「打开链接」按钮 |
| `buttons` | table | 无 | 最多 5 个，元素为 `{label=..., ...action}` |

动作表与 §6.3 一致，例如：

```lua
on_click = { copy = "123456", sensitive = true }
buttons = {
  { label = "复制验证码", copy = "123456", sensitive = true },
  { label = "打开链接", open = "https://example.com" },
}
```

### 8.5 host API

| API | 说明 |
| --- | --- |
| `ntfy.log(level, msg)` | 写日志，自动脱敏 |
| `ntfy.json.decode(s)` / `ntfy.json.encode(v)` | JSON |
| `ntfy.match(s, pattern)` / `ntfy.gsub(s, pattern, repl)` | Go RE2 正则，非 Lua pattern |
| `ntfy.now()` | Unix 时间戳 |
| `ntfy.version` | 引擎 API 版本 |
| `ctx.kv` | 持久化键值（JSON 可序列化），按 topic 隔离 |

### 8.6 沙箱

- `lua.NewState(lua.Options{SkipOpenLibs: true})`，手动只开 `base/table/string/math`。
- 裁剪后的 `os`：只保留 `time/date/clock`，去掉 `execute/exit/remove/rename/getenv/tmpname`。
- 不提供 `io`、`package`、`debug`、`loadfile`、`dofile`、`load`、`require`。
- 不提供网络能力；需要 HTTP 的场景走 `http` 动作，由 Go 侧白名单执行。
- 脚本是用户自己写的本地代码，沙箱目标是防误操作，不是防恶意代码。

### 8.7 示例：短信验证码

`scripts/sms.lua`：

```lua
local function present(s)
  return s ~= nil and s ~= ""
end

function on_message(m)
  local ok, d = pcall(ntfy.json.decode, m.message)
  if not ok or type(d) ~= "table" or not present(d.vcode) then
    ntfy.log("warn", "sms 解析失败: " .. m.id)
    return { title = "短消息", body = m.message }
  end

  local parts = {}
  if present(d.name)    then table.insert(parts, d.name)    end
  if present(d.sent_by) then table.insert(parts, d.sent_by) end
  if #parts == 0        then parts[1] = "未知发送方"         end
  local first = table.concat(parts, " · ")

  return {
    title    = "验证码 " .. d.vcode,
    body     = first .. "\n" .. (d.timestamp or ""),
    priority = 4,
    sound    = "mail",
    on_click = { copy = d.vcode, sensitive = true },
    buttons  = {
      { label = "复制验证码", copy = d.vcode, sensitive = true },
    },
  }
end
```

输入（示例）：

```json
{
  "vcode": "123456",
  "name": "示例公司",
  "sent_by": "10690000000000",
  "timestamp": "2026-01-01 12:00:00"
}
```

约定：

- `vcode` 始终按字符串处理，不转数字，保留前导零。
- `name` 为空时正文退化为 `sent_by`，不出现 ` · ` 空拼接。
- 解析失败弹原始正文，宁可难看也不吞消息。
- 日志中 `vcode` 脱敏为 `778***`。
- 该 topic 默认不限流（`max_per_min = 0`），避免丢掉验证码。

## 9. 默认映射（无脚本）

| ntfy | Toast |
| --- | --- |
| `title` 为空 | `[<topic>]` |
| `message` | 正文，超过 `long_body_limit`（默认 300 字）截断并加 `…` |
| `priority` 1–2 | `silent` + `short` |
| `priority` 3 | `default` + `short` |
| `priority` 4 | `mail` + `short` |
| `priority` 5 | `default` + `long` |
| `tags` | 取常见标签加前缀符号（error/warning/white_check_mark 等），其余忽略 |
| `click` | 整条通知点击打开（http/https 白名单） |
| `actions` 中 `view` | 转为按钮（最多 3 个） |
| `attachment` | v1 忽略（不下载、不展示） |

补充：

- Toast 正文大约 4 行后折叠，长消息靠点击复制全文（脚本或默认动作）。
- 不做 Markdown 渲染；代码围栏标记只做简单去除。
- 同一通知的 `tag/group` 去重、替换暂不支持（`winrt-toast` 未暴露），需要时 fork 补。

## 10. 配置

默认读取 exe 同目录 `config.toml`；`--config` 可覆盖。脚本目录相对于配置文件所在目录。

```toml
[app]
name      = "ntfy Notifier"
app_id    = "com.example.ntfynotifier"
scheme    = "ntfy-notifier"
autostart = true

[server]
base_url = "https://ntfy.example.com"
token    = "tk_xxx"
# username = ""
# password = ""
# tls_ca   = "ca.pem"
# mode     = "stream"          # stream | poll
# proxy    = "none"

[notify]
max_per_min     = 10           # 全局；0 表示不限
quiet_hours     = ""           # 例如 "23:30-08:00"
quiet_action    = "silent"     # silent | drop
long_body_limit = 300

[actions]
allow_open       = ["http", "https"]
allow_http       = true
http_allow_hosts = ["127.0.0.1"]
allow_run        = false
pending_ttl      = "10m"

[scripts]
dir     = "scripts"
timeout = "200ms"

[[topic]]
name        = "sms"
max_per_min = 0
# script    = "scripts/sms.lua"   # 默认自动匹配

[[topic]]
name = "agent"
```

路径约定：

- 配置：exe 目录（或 `--config`）。
- 状态：`%APPDATA%\ntfy-notifier\state.json`。
- pending：`%LOCALAPPDATA%\ntfy-notifier\pending\`。
- 日志：`%APPDATA%\ntfy-notifier\logs\app.log`，5MB × 3。

## 11. 状态存储

`state.json` 结构：

```json
{
  "v": 1,
  "lastSeen": { "sms": 1795564800, "agent": 1795563900 },
  "kv": { "sms": { "count": 12 } }
}
```

- 原子写：临时文件 + `os.Rename`。
- 变更后 2s 防抖落盘，退出时强制 flush。
- 损坏时备份为 `state.json.bak` 并从空状态启动（日志告警）。

## 12. 托盘与生命周期

- 单实例：`CreateMutexW("Local\\ntfy-notifier")`，已存在则提示并退出。
- 菜单：
  - 状态：已连接 / 重连中 / 已暂停 / 脚本错误
  - 暂停 15 分钟 / 1 小时 / 直到明天
  - 立即重连
  - 重载配置与脚本
  - 打开配置 / 脚本目录 / 日志目录
  - 最近事件（最近 10 条，子菜单）
  - 开机自启（勾选，写 HKCU Run）
  - 关于 / 退出
- 开机自启：`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`，值为 `"<exe>" --autostart`。
- 构建使用 `-H=windowsgui`，无控制台窗口；`--console` 开发时输出到 stderr。
- 启动顺序：解析 `--activate` → 单实例 → 配置 → 注册 scheme/AUMID → 订阅 → 托盘。

## 13. 错误处理与可观测性

- 所有外部失败（HTTP、Toast、剪贴板、脚本）都要有日志，不允许静默失败。
- 日志级别：`debug/info/warn/error`，默认 `info`；`--debug` 打开调试。
- 脱敏：`vcode`、token、pending 中的 `text` 不进 info 日志。
- 托盘 tooltip 显示：连接状态、各 topic 最后消息时间、丢弃计数。
- 计数指标：received / notified / suppressed / dropped / script_error / toast_error。

## 14. 测试策略

单元测试（Linux 可跑）：

- 配置解析与校验、默认值、topic 冲突检测。
- NDJSON 解码、事件过滤、`since` 计算、去重 LRU。
- 令牌桶与静音时段。
- Lua 契约：字段转换、动作表解析、沙箱（`io`/`os.execute` 不可用）、超时后 VM 重建。
- 动作校验：scheme/host 白名单、run 关闭、路径穿越。
- pending TTL 与清理、状态原子写。
- 截断按 rune 计数，不切坏中文。

集成测试：

- `docker compose` 起本地 ntfy，发布脚本覆盖多 topic、JSON 正文、重连。
- `--test-message <json>` 把一条消息喂进完整管线（不联网），便于脚本调试。
- `--once` 之类的一次性运行模式，方便 CI。

Windows 手工清单：

- AUMID 显示名与图标正确。
- 点击「复制验证码」后剪贴板内容正确，Win+V 中不出现。
- 点通知主体与点按钮都只启动一个短命进程，无窗口闪现。
- 专注助手开启时通知被抑制（预期行为）。
- 断网 2 分钟、睡眠唤醒后能续传。
- 开机自启生效，卸载/`--unregister` 后注册表清理干净。

CI：

- Linux：`go vet` + 单元测试。
- Windows runner：构建 + 单元测试 + 一个真实 Toast 冒烟（允许失败标记）。
- 产物：`ntfy-notifier-windows-amd64.zip`、`windows-arm64.zip`。

## 15. 打包与分发

- 目录：

```
ntfy-notifier.exe
config.toml
scripts/
  sms.lua
assets/icon.png
README.md
```

- 图标：`go-winres` 或 `rsrc` 嵌入 `.ico`；托盘图标用 PNG。
- Manifest：Per-Monitor V2 DPI aware，`asInvoker`。
- 可选 Inno Setup 安装包；卸载时删除 scheme、Run 键、`%APPDATA%` 数据（询问用户）。
- 未签名 exe 会有 SmartScreen 提示，文档说明；有条件再做代码签名。

## 16. 里程碑与验收

| 阶段 | 内容 | 验收标准 | 预估 |
| --- | --- | --- | --- |
| M0 | 骨架、配置、单 topic 订阅、Toast、AUMID、`--test-toast` | 真机看到带应用名和图标的通知 | 0.5 天 |
| M0.5 | scheme 注册、`--activate`、pending、剪贴板 | 点测试通知，剪贴板拿到内容；无窗口闪现；pending 文件被删；Win+V 不记录 | 0.5–1 天 |
| M1 | 多 topic、重连、`since`、去重、state、托盘 | 断网 2 分钟恢复后不重复、不漏；托盘状态正确 | 1 天 |
| M2 | Lua 引擎、自动挂载、热重载、动作执行器 | 放入 `sms.lua` 立即生效；语法错误时旧版本继续工作 | 1–2 天 |
| M3 | 默认映射完善、限流、静音、自启、单实例、日志 | 短信不限流；高频 topic 合并/丢弃有计数；开机自启生效 | 1 天 |
| M4 | 打包、卸载清理、README、冒烟 | 绿色包解压即用；卸载后注册表干净 | 0.5 天 |

M0.5 必须最先验证，因为 protocol 激活与剪贴板只能在真实 Windows 上确认。

## 17. 风险与应对

| 风险 | 影响 | 应对 |
| --- | --- | --- |
| `winrt-toast` 较新、采用率低 | Toast 异常/API 变更 | 包在 `Notifier` 接口后；必要时 fork（补 Tag/Group/Scenario） |
| protocol 激活在个别版本不触发 | 点击复制失效 | M0.5 真机验证；失败则升级 COM 激活服务器，或退化为仅按钮 |
| 通知中心残留 | 点击后仍显示 | fork 补 `Tag/Group` + `History.Remove`；或 `History.Clear`（慎用） |
| 专注助手/通知关闭 | 用户收不到 | 文档说明；v1 不尝试绕过；priority 5 未来可加 urgent |
| 剪贴板被占用 | 复制失败 | 重试 + 日志；失败时可选弹一条提示 |
| ntfy 缓存时间短 | 长时间离线丢消息 | 文档说明；`since` 尽力续传，不承诺 |
| gopher-lua 是 5.1 | 无 `utf8` 库、位运算差异 | 提供 `ntfy` 辅助函数；文档标注 |
| 脚本死循环 | topic 卡死 | 200ms 超时 + VM 重建；worker 队列有界 |
| 未签名 exe | SmartScreen 警告 | README 说明；后续代码签名 |
| Go 1.25 要求 | 开发机需升级 | WSL 安装最新工具链；CI 固定版本 |

## 18. 待确认

1. 短信 topic 的实际名称、coding agent topic 的名称。
2. 通知中是否直接显示验证码，还是显示「验证码已收到，点击复制」（防偷窥）。
3. `priority = 4` + `mail` 提示音是否合适。
4. 应用显示名与图标：暂定 `ntfy Notifier` + 占位图。
5. 剪贴板中的验证码是否需要在 60 秒后自动清空（需要主进程定时器 + IPC）。

## 19. 决策记录

| 决策 | 结论 | 理由 |
| --- | --- | --- |
| 点击回传方式 | 自定义 URI scheme + 短命实例 | 免管理员、无需 COM、可执行任意本地动作 |
| 订阅连接粒度 | 每 `(server, token/username)` 一条多 topic 流 | 减少连接数；配合时间戳 `since` 与去重 |
| 脚本映射 | `scripts/<topic>.lua` | 零配置、所见即所得；TOML 可覆盖 |
| VM 模型 | 每 topic 一个串行 VM | `LState` 非并发安全；状态隔离 |
| 动作执行 | 落盘 + uuid | 避免 URI 长度/编码/泄露问题，支持非 ASCII 与长文本 |
| Toast 库 | `winrt-toast` | 纯 Go、无 PowerShell、支持 protocol 激活 |
| 限流 | 全局 + 每 topic，默认对短信关闭 | 验证码不可丢；高频 topic 需要保护 |
