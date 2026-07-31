# Xiaoli Server on Fly.io

This directory deploys a Go-only Xiaoli device/admin backend to Fly.io.

The container runs a single Go process on port `8080`:

- `https://<app>.fly.dev/xiaozhi/ota/` issues the board WebSocket config
- `wss://<app>.fly.dev/xiaozhi/v1/` accepts the board WebSocket connection and MCP calls
- `https://<app>.fly.dev/lark/events` accepts Lark message events when `LARK_APP_ID` and `LARK_APP_TOKEN` are configured
- `https://<app>.fly.dev/mcp/vision/snapshot` and `/mcp/vision/stream/frame` receive camera uploads
- `https://<app>.fly.dev/admin` serves the Admin console
- Voice chat receives board Opus audio, runs ASR -> LLM/VLLM -> TTS, and asks the board to play Ogg Opus through `self.audio_speaker.play_ogg_url`
- Admin text playback uses the same Go TTS/playback path

## First Deploy

Install and log in:

```bash
brew install flyctl
fly auth login
```

Create the app once:

```bash
fly apps create xiaoli-server
```

If you change the app name, update `app` and `PUBLIC_BASE_URL` in `server/fly.toml`.

Set required model secrets. For the current defaults, the minimum useful set is:

```bash
fly secrets set NVIDIA_API_KEY=your_nvidia_key
fly secrets set OPENROUTER_API_KEY=your_openrouter_key
fly secrets set SILICONFLOW_API_KEY=your_siliconflow_key
fly secrets set SERVER_AUTH_KEY=$(openssl rand -hex 32)
fly secrets set ADMIN_SESSION_SECRET=$(openssl rand -hex 32)
fly secrets set LOGTO_APP_SECRET=your_logto_app_secret
fly secrets set STUDY_MONITOR_ENABLED=true LARK_BOT_WEBHOOK_URL=your_lark_webhook LARK_APP_ID=your_lark_app_id LARK_APP_TOKEN=your_lark_app_token
```

Server authentication is enabled by default. OTA is left reachable so legacy
ESP32 devices can check updates and fetch the current WebSocket URL.
`ALLOWED_DEVICE_IDS` is only the legacy ESP32 allowlist; keep
`SERVER_AUTH_KEY` as a Fly secret and rotate it if it is ever exposed.

The admin console and device protocol are implemented in Go. Logto is the only
login path. Configure Logto with callback URL:

```text
https://xiaoli-server.fly.dev/admin/callback
```

and post-logout redirect URL:

```text
https://xiaoli-server.fly.dev/admin
```

Rotate the Logto app secret if it is ever exposed.

## Android 平板扫码配对

已登录 Logto 的管理员可创建一次性配对码：

```bash
curl -X POST --cookie "xiaoli_admin_session=..." \
  https://xiaoli-server.fly.dev/admin/api/device-pairings
```

响应中的 `qr_payload`（`pair_url` 与 5 分钟有效的 `code`）直接编码为二维码；管理台的“添加学习平板”按钮会显示本地生成的 `qr_image`。Android 应用扫描后向 `pair_url` 提交 `code`、`device_id`、`device_name` 和 `device_kind=android`；服务端返回专属 WebSocket URL 和设备 token。

Android 设备凭证以 token 哈希持久化到 `/data/paired_devices.json`，并关联到创建配对码的 Logto `sub`。它们不需要加入 `ALLOWED_DEVICE_IDS`，且不会使用 ESP32 的全局 `SERVER_AUTH_KEY`。现有 ESP32 保持原有静态白名单和全局 token 流程不变。

Continuous deployment is handled by `.github/workflows/fly-deploy.yml`: a push
to `main` that changes server files builds and deploys the service. The
workflow resolves the newest stable `cyeam-cli` Git tag and passes it as
`CYEAM_VERSION`, so Docker reuses the cyeam download layer until a new release
is published. The Docker build context is the repository root because both
Server and TUI share the root `internal` package.

`XIAOLI_SKILLS` defaults to floating installs such as `mnhkahn/cyeam-cli`.
The workflow uses the commit SHA to refresh the skill install layer without
invalidating the separately cached cyeam binary.

Check health:

```bash
curl https://xiaoli-server.fly.dev/health
curl https://xiaoli-server.fly.dev/xiaozhi/ota/
```

## Local TUI

The local terminal UI has moved to `../tui`. See `../tui/README.md` for installation and usage.

## Firmware OTA URL

After the Fly app is deployed, rebuild the firmware with:

```text
CONFIG_OTA_URL="https://xiaoli-server.fly.dev/xiaozhi/ota/"
```

Then flash the board.

## Configuration

Important environment variables:

- `PUBLIC_BASE_URL`: public HTTPS base URL, for example `https://xiaoli-server.fly.dev`
- `ENABLE_SERVER_AUTH`: default `true`; OTA issues WebSocket tokens and the WebSocket server verifies them
- `ALLOWED_DEVICE_IDS`: comma-separated device IDs allowed through Nginx for WebSocket and vision requests
- Android devices created through `/admin/api/device-pairings` are dynamically authorized and do not belong in `ALLOWED_DEVICE_IDS`
- `SERVER_AUTH_KEY`: signing key for WebSocket and vision tokens; set as a secret
- `SERVER_AUTH_ALLOWED_DEVICE_IDS`: optional upstream bypass list; normally leave empty so token verification is not bypassed
- `XIAOLI_ADMIN_ENABLED`: enables the admin console when `true`
- `XIAOLI_ADMIN_PORT`: Go server port; default `8080` on Fly
- `XIAOLI_DIRECT_DEVICE_SERVER`: when `true`, Admin controls the board directly through Go instead of the old bridge
- `ADMIN_SESSION_SECRET`: signing key for admin cookies; set as a secret
- `LOGTO_ENDPOINT`: Logto tenant endpoint, for example `https://fpilyb.logto.app/`
- `LOGTO_APP_ID`: Logto application ID
- `LOGTO_APP_SECRET`: Logto application secret; set as a secret
- `ADMIN_ALLOWED_USERS`: optional comma-separated Logto sub/email/username/name allowlist; `*` allows all authenticated users
- `LARK_BOT_WEBHOOK_URL`: custom bot webhook URL; set as a secret
- `LARK_APP_ID`: Lark app ID; when set together with `LARK_APP_TOKEN`, enables `/lark/events`
- `LARK_APP_TOKEN`: Lark app token used as the app credential for tenant access tokens; set as a secret
Model and MCP settings:

- `settings.json`: stores non-secret model endpoints, model names, `/model` options, ASR/Vision/TTS settings, and MCP server URLs. Each model option can set `max_tokens` (default 180) and `api_key_env` to reference a Fly secret.
- `AGENT.md`: stores the default agent prompt. Optional `SOUL.md` is appended when present.
- `SILICONFLOW_API_KEY`: secret used by the default settings via `api_key_env`.
- Other provider keys, such as `NVIDIA_API_KEY`, `OPENROUTER_API_KEY` or `OPENAI_API_KEY`, can be referenced from `settings.json` with `api_key_env`.
- The Docker image copies `settings.json` and `AGENT.md` to `/opt/xiaoli/`, alongside `/opt/xiaoli/skills`.

Skill support:

- The Docker image installs every skill from build arg `XIAOLI_SKILLS` into `/opt/xiaoli/skills`; default is `mnhkahn/cyeam-cli`, which currently contributes multiple skills such as `cyeam-cli`, `live-broadcast`, `holiday`, `mo`, `roadbook`, and related cyeam workflows.
- Executable files under each skill's `bin/` directory are copied to `/usr/local/bin`. The image also installs the `cyeam` binary for `cyeam-cli` if the skill package does not include one.
- `XIAOLI_SKILL_ROOTS`: comma-separated skill roots; default `/opt/xiaoli/skills`.
- `XIAOLI_AGENTS_DIR`: optional persistent `.agents` directory. When set, startup initializes its `skills` directory from `/opt/xiaoli/skills` if empty and links the current user's `~/.agents` to it. Fly uses `/data/.agents` so `cyeam update` survives machine replacement.
- `XIAOLI_ENABLED_SKILLS`: comma-separated allowlist; default `*` enables every indexed skill.
- `XIAOLI_SKILL_MAX_BYTES`: maximum bytes per loaded `SKILL.md`; default `65536`.
- `XIAOLI_SKILL_EXEC_TIMEOUT_SECONDS`: maximum runtime for a skill CLI invocation; default `8`.
- `XIAOLI_SKILL_EXEC_MAX_OUTPUT_BYTES`: maximum captured stdout bytes; default `262144`.
- `XIAOLI_SKILL_EXEC_GLOBAL_BIN_DIRS`: comma-separated global executable allowlist; default `/usr/local/bin`.
- At startup the server indexes only Skill frontmatter. During an agent run, Eino exposes a `skill` tool; the model calls it with a skill name to lazily load the full `SKILL.md`.

Use `fly secrets set` for keys. Do not commit real keys.

## Memory（用户记忆）

Xiaoli 是单用户系统。记忆存储在 Redis，每轮对话自动加载。

两种作用域（LLM 工具的 `scope` 参数可选，默认 `global`）：

| 作用域 | Redis Key | 说明 |
|--------|-----------|------|
| `global` | `{prefix}memory:global` | 全局共享。不区分频道和用户，所有设备/频道共享同一份记忆 |
| `channel` | `{prefix}memory:{channel}:{user}` | 频道内独立。比如 Lark 和 ESP32 的记忆不互通 |

**关键假设**：系统是单人使用的，`global` 没有全局用户 ID。如果将来需要多用户隔离，需引入全局用户标识（如 Logto sub）。

## Cron 定时任务

Cron 任务配置在 `settings.json` 的 `"cron"` 字段，不再是环境变量。支持两种触发器：

- **interval 型**（`every` + 可选 `start_hour`/`end_hour` 时间窗）
- **固定时间型**（`at_hour` + `at_minute`，必须成对出现）

示例：

```json
"cron": {
  "study_monitor": {
    "enabled": false,
    "trigger": { "every": "5m", "timezone": "Asia/Shanghai", "start_hour": 17, "end_hour": 21 },
    "agent": { "name": "dispatch_agent", "mode": "react", "max_steps": 6, "timeout": "150s" },
    "metadata": { "camera_tool": "self.camera.take_photo", "reminder_text": "请坐直，认真学习。", "tool_timeout": "120s" }
  },
  "morning_greeting": {
    "enabled": true,
    "trigger": { "at_hour": 8, "at_minute": 0, "timezone": "Asia/Shanghai" },
    "agent": { "name": "dispatch_agent", "mode": "react", "max_steps": 4, "timeout": "120s" },
    "metadata": { "text": "早上好。" }
  }
}
```

斜杠命令：`/cron list` 查看定时任务，`/cron run <任务ID>` 立即执行。

## MCP 外部服务

`settings.json` 的 `"mcp_servers"` 支持 `api_key_env` 字段引用环境变量中的 API key：

```json
{
  "name": "AMap",
  "url": "https://mcp.amap.com/mcp",
  "api_key_env": "AMAP_API_KEY"
}
```

Key 由 MCP 客户端在请求时静默注入 URL query 参数，不进入日志。`api_key_env` 非空但未配置时跳过该服务。

配置方式：

```bash
fly secrets set AMAP_API_KEY=你的高德APIKey
```
