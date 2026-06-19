# Xiaoli Server on Fly.io

![Architecture](../docs/architecture.svg)

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
cd server
fly apps create xiaoli-server
```

If you change the app name, update `app` and `PUBLIC_BASE_URL` in `fly.toml`.

Set required model secrets. For the current defaults, the minimum useful set is:

```bash
fly secrets set OPENROUTER_API_KEY=your_openrouter_key
fly secrets set SILICONFLOW_API_KEY=your_siliconflow_key
fly secrets set SERVER_AUTH_KEY=$(openssl rand -hex 32)
fly secrets set ADMIN_SESSION_SECRET=$(openssl rand -hex 32)
fly secrets set LOGTO_APP_SECRET=your_logto_app_secret
fly secrets set STUDY_MONITOR_ENABLED=true LARK_BOT_WEBHOOK_URL=your_lark_webhook LARK_APP_ID=your_lark_app_id LARK_APP_TOKEN=your_lark_app_token
```

Server authentication is enabled by default. OTA is left reachable so devices
can check updates and fetch the current WebSocket URL. `ALLOWED_DEVICE_IDS` is
used by the Nginx edge gate for WebSocket and vision requests; it is not
rendered into the upstream `server.auth.allowed_devices` list, because that
upstream list bypasses token verification. Keep `SERVER_AUTH_KEY` as a Fly
secret and rotate it if it is ever exposed.

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

The study monitor is optional. When `STUDY_MONITOR_ENABLED=true`, the admin
server runs a background job in `Asia/Shanghai` time from 17:00 to 21:00 every
5 minutes. Each run asks the device camera tool to inspect study posture, sends
the captured image and analysis to the Lark bot, and calls a speaker/TTS tool
when a reminder is needed.

Deploy:

```bash
fly deploy
```

Check health:

```bash
curl https://xiaoli-server.fly.dev/health
curl https://xiaoli-server.fly.dev/xiaozhi/ota/
```

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
- `STUDY_MONITOR_ENABLED`: enables the study monitor background job when `true`; set as a secret in production
- `STUDY_MONITOR_TIMEZONE`: default `Asia/Shanghai`
- `STUDY_MONITOR_START_HOUR`: default `17`
- `STUDY_MONITOR_END_HOUR`: default `21`
- `STUDY_MONITOR_INTERVAL_SECONDS`: default `300`
- `STUDY_MONITOR_CAMERA_TOOL`: camera tool name; default `self.camera.take_photo`
- `STUDY_MONITOR_TOOL_TIMEOUT_SECONDS`: camera tool timeout; default `120`
- `STUDY_MONITOR_REMINDER_TEXT`: speaker reminder text when posture/focus needs correction
- `LARK_BOT_WEBHOOK_URL`: custom bot webhook URL; set as a secret
- `LARK_APP_ID`: Lark app ID; when set together with `LARK_APP_TOKEN`, enables `/lark/events`
- `LARK_APP_TOKEN`: Lark app token used as the app credential for tenant access tokens; set as a secret
Model and MCP settings:

- `settings.json`: stores non-secret model endpoints, model names, `/model` options, ASR/Vision/TTS settings, and MCP server URLs.
- `AGENT.md`: stores the default agent prompt. Optional `SOUL.md` is appended when present.
- `SILICONFLOW_API_KEY`: secret used by the default settings via `api_key_env`.
- Other provider keys, such as `OPENROUTER_API_KEY` or `OPENAI_API_KEY`, can be referenced from `settings.json` with `api_key_env`.
- The Docker image copies `settings.json` and `AGENT.md` to `/opt/xiaoli/`, alongside `/opt/xiaoli/skills`.

Skill support:

- The Docker image installs every skill from build arg `XIAOLI_SKILLS` into `/opt/xiaoli/skills`; default is `mnhkahn/cyeam-cli`, which currently contributes multiple skills such as `cyeam-cli`, `live-broadcast`, `holiday`, `mo`, `roadbook`, and related cyeam workflows.
- Executable files under each skill's `bin/` directory are copied to `/usr/local/bin`. The image also installs the `cyeam` binary for `cyeam-cli` if the skill package does not include one.
- `XIAOLI_SKILL_ROOTS`: comma-separated skill roots; default `/opt/xiaoli/skills`.
- `XIAOLI_ENABLED_SKILLS`: comma-separated allowlist; default `*` enables every indexed skill.
- `XIAOLI_SKILL_MAX_BYTES`: maximum bytes per loaded `SKILL.md`; default `65536`.
- `XIAOLI_SKILL_EXEC_TIMEOUT_SECONDS`: maximum runtime for a skill CLI invocation; default `8`.
- `XIAOLI_SKILL_EXEC_MAX_OUTPUT_BYTES`: maximum captured stdout bytes; default `262144`.
- `XIAOLI_SKILL_EXEC_GLOBAL_BIN_DIRS`: comma-separated global executable allowlist; default `/usr/local/bin`.
- At startup the server indexes only Skill frontmatter. During an agent run, Eino exposes a `skill` tool; the model calls it with a skill name to lazily load the full `SKILL.md`.

Use `fly secrets set` for keys. Do not commit real keys.
