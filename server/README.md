# Xiaoli Server on Fly.io

<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 900 780" font-family="Menlo, Monaco, 'Courier New', monospace">
  <defs>
    <linearGradient id="in-progress" x1="0" y1="0" x2="1" y2="0">
      <stop offset="0%" stop-color="#f59e0b"/>
      <stop offset="100%" stop-color="#fbbf24"/>
    </linearGradient>
    <linearGradient id="done" x1="0" y1="0" x2="1" y2="0">
      <stop offset="0%" stop-color="#10b981"/>
      <stop offset="100%" stop-color="#34d399"/>
    </linearGradient>
    <filter id="shadow" x="-2%" y="-2%" width="104%" height="108%">
      <feDropShadow dx="0" dy="2" stdDeviation="3" flood-opacity="0.15"/>
    </filter>
  </defs>

  <rect width="900" height="780" fill="#1a1b2e" rx="12"/>
  <text x="450" y="32" text-anchor="middle" fill="#ffffff" font-size="17" font-weight="bold">Xiaoli Server — 分层架构</text>

  <line x1="450" y1="42" x2="450" y2="58" stroke="#555" stroke-width="1"/>

  <!-- 入口点层 -->
  <rect x="120" y="58" width="660" height="62" rx="8" fill="url(#done)" filter="url(#shadow)" opacity="0.95"/>
  <text x="145" y="80" fill="#064e3b" font-size="13" font-weight="bold">入口点层</text>
  <text x="145" y="98" fill="#064e3b" font-size="11" opacity="0.85">cmd/xiaoli-admin/main.go → LoadConfig() → NewServer(cfg) → http.ListenAndServe</text>
  <text x="145" y="112" fill="#064e3b" font-size="10" opacity="0.65">背景任务：StudyMonitor · Lark WS Client · WeChat 轮询</text>

  <line x1="450" y1="120" x2="450" y2="140" stroke="#10b981" stroke-width="2"/>

  <!-- HTTP 路由层 -->
  <rect x="30" y="140" width="840" height="72" rx="8" fill="url(#done)" filter="url(#shadow)" opacity="0.95"/>
  <text x="55" y="162" fill="#064e3b" font-size="13" font-weight="bold">HTTP 路由层</text>
  <text x="55" y="182" fill="#064e3b" font-size="11" opacity="0.85">server.go — ServeHTTP 多路分发</text>
  <text x="55" y="200" fill="#064e3b" font-size="10" opacity="0.65">/health · /xiaozhi/ota/ · /xiaozhi/v1/(WS) · /lark/events · /mcp/vision/ · /admin/*</text>

  <line x1="450" y1="212" x2="450" y2="232" stroke="#10b981" stroke-width="2"/>

  <!-- 渠道层 -->
  <rect x="30" y="232" width="840" height="72" rx="8" fill="url(#done)" filter="url(#shadow)" opacity="0.95"/>
  <text x="55" y="254" fill="#064e3b" font-size="13" font-weight="bold">渠道层</text>
  <text x="55" y="274" fill="#064e3b" font-size="11" opacity="0.85">agent/channel · channel.go — Provider 注册 · ConversationPipeline</text>
  <text x="55" y="292" fill="#064e3b" font-size="10" opacity="0.65">agent/channel/lark (WS) · agent/channel/wechat (轮询) · agent/esp32/hub (Device WS) · agent/slash (斜杠命令)</text>

  <line x1="135" y1="304" x2="135" y2="324" stroke="#10b981" stroke-width="2"/>
  <line x1="450" y1="304" x2="450" y2="324" stroke="#10b981" stroke-width="2"/>
  <line x1="765" y1="304" x2="765" y2="324" stroke="#10b981" stroke-width="2"/>

  <!-- 编排层 -->
  <rect x="30" y="324" width="270" height="100" rx="8" fill="url(#in-progress)" filter="url(#shadow)" opacity="0.95"/>
  <text x="55" y="346" fill="#1a1b2e" font-size="12" font-weight="bold">编排层</text>
  <text x="55" y="364" fill="#1a1b2e" font-size="10">direct_ai.go — EinoAgent</text>
  <text x="55" y="380" fill="#1a1b2e" font-size="10">conversation.go — ConversationPipeline</text>
  <text x="55" y="396" fill="#1a1b2e" font-size="9" opacity="0.65">Eino CloudWeGo 框架</text>
  <text x="55" y="410" fill="#1a1b2e" font-size="9" opacity="0.65">ChatModelAgent · 工具循环 · 技能注入</text>

  <!-- AI 服务层 -->
  <rect x="315" y="324" width="270" height="100" rx="8" fill="url(#done)" filter="url(#shadow)" opacity="0.95"/>
  <text x="340" y="346" fill="#064e3b" font-size="12" font-weight="bold">AI 服务层</text>
  <text x="340" y="364" fill="#064e3b" font-size="10">ASR: direct_ai.go — OpenAITranscriber</text>
  <text x="340" y="380" fill="#064e3b" font-size="10">VLLM: direct_vision.go — GoVisionClient</text>
  <text x="340" y="396" fill="#064e3b" font-size="10">TTS: direct_tts.go — HTTPSpeechSynthesizer</text>
  <text x="340" y="410" fill="#064e3b" font-size="9" opacity="0.65">SiliconFlow / OpenRouter 多 provider</text>

  <!-- 工具层 -->
  <rect x="600" y="324" width="270" height="100" rx="8" fill="url(#in-progress)" filter="url(#shadow)" opacity="0.95"/>
  <text x="625" y="346" fill="#1a1b2e" font-size="12" font-weight="bold">工具层</text>
  <text x="625" y="364" fill="#1a1b2e" font-size="10">agent/tool/skill — Skill 后端</text>
  <text x="625" y="380" fill="#1a1b2e" font-size="10">agent/tool/mcp — MCP 客户端</text>
  <text x="625" y="396" fill="#1a1b2e" font-size="9" opacity="0.65">skill CLI 执行 · MCP HTTP/SSE 连接</text>
  <text x="625" y="410" fill="#1a1b2e" font-size="9" opacity="0.65">设备 MCP 桥接</text>

  <line x1="165" y1="424" x2="165" y2="448" stroke="#f59e0b" stroke-width="2"/>
  <line x1="450" y1="424" x2="450" y2="448" stroke="#10b981" stroke-width="2"/>
  <line x1="735" y1="424" x2="735" y2="448" stroke="#f59e0b" stroke-width="2"/>
  <line x1="165" y1="448" x2="735" y2="448" stroke="#f59e0b" stroke-width="1.5"/>
  <line x1="450" y1="448" x2="450" y2="468" stroke="#f59e0b" stroke-width="2"/>

  <!-- 基础设施层 -->
  <rect x="30" y="468" width="840" height="72" rx="8" fill="url(#done)" filter="url(#shadow)" opacity="0.95"/>
  <text x="55" y="490" fill="#064e3b" font-size="13" font-weight="bold">基础设施层</text>
  <text x="55" y="510" fill="#064e3b" font-size="11" opacity="0.85">Redis 记忆 (direct_ai.go) · Logto OIDC 认证 (server.go) · 配置管理 (config.go)</text>
  <text x="55" y="528" fill="#064e3b" font-size="10" opacity="0.65">Stream Hub 摄像头帧转发 · HMAC Cookie 签名 · 设备认证 Token</text>

  <line x1="450" y1="540" x2="450" y2="560" stroke="#10b981" stroke-width="2"/>

  <!-- ESP32 设备层 -->
  <rect x="30" y="560" width="840" height="65" rx="8" fill="url(#done)" filter="url(#shadow)" opacity="0.95"/>
  <text x="55" y="582" fill="#064e3b" font-size="13" font-weight="bold">设备协议层</text>
  <text x="55" y="600" fill="#064e3b" font-size="11" opacity="0.85">agent/esp32 — DeviceHub · 会话管理 · MCP 代理 · Voice 处理</text>
  <text x="55" y="616" fill="#064e3b" font-size="10" opacity="0.65">agent/esp32/audio — Ogg Opus 编解码 · VadSilero 语音活动检测</text>

  <line x1="450" y1="625" x2="450" y2="643" stroke="#10b981" stroke-width="2"/>

  <!-- 外部依赖层 -->
  <rect x="100" y="643" width="700" height="50" rx="8" fill="#252640" opacity="0.9" stroke="#4b5563" stroke-width="1"/>
  <text x="450" y="665" text-anchor="middle" fill="#9ca3af" font-size="11">外部依赖</text>
  <text x="450" y="682" text-anchor="middle" fill="#6b7280" font-size="10">SiliconFlow (ASR/LLM/VLLM/TTS) · OpenRouter · Fly.io (nrt) · Redis · Logto · Lark · 微信</text>

  <!-- 图例 -->
  <rect x="300" y="708" width="300" height="32" rx="6" fill="#252640" opacity="0.9"/>
  <rect x="315" y="718" width="14" height="14" rx="3" fill="url(#done)"/>
  <text x="335" y="730" fill="#34d399" font-size="10">已实现</text>
  <rect x="415" y="718" width="14" height="14" rx="3" fill="url(#in-progress)"/>
  <text x="435" y="730" fill="#fbbf24" font-size="10">迭代中</text>
</svg>

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
- `ASR_MODULE`: default `SiliconFlowASR`
- `LLM_MODULE`: default `SiliconFlowLLM`
- `VLLM_MODULE`: default `SiliconFlowVLLM`
- `TTS_MODULE`: default `SiliconFlowTTS`
- `OPENROUTER_API_KEY`: used by `OpenRouterLLM` and `OpenRouterVLLM`
- `OPENROUTER_LLM_MODEL`: default `openrouter/free`
- `OPENROUTER_VLLM_MODEL`: default `openrouter/free`
- `SILICONFLOW_API_KEY`: used by the Go ASR/LLM/VLLM/TTS clients by default
- `SILICONFLOW_LLM_MODEL`: default `Qwen/Qwen3-8B`
- `SILICONFLOW_VLLM_MODEL`: default `Qwen/Qwen3-VL-8B-Instruct`
- `SILICONFLOW_ASR_MODEL`: default `FunAudioLLM/SenseVoiceSmall`
- `SILICONFLOW_TTS_MODEL`: default `FunAudioLLM/CosyVoice2-0.5B`
- `SILICONFLOW_TTS_VOICE`: default `FunAudioLLM/CosyVoice2-0.5B:anna`
- `XIAOLI_GO_ASR_URL`: OpenAI-compatible transcription endpoint; default `https://api.siliconflow.cn/v1/audio/transcriptions`
- `XIAOLI_GO_ASR_MODEL`: default comes from `SILICONFLOW_ASR_MODEL`
- `XIAOLI_GO_LLM_URL`: OpenAI-compatible chat completions endpoint; default `https://api.siliconflow.cn/v1/chat/completions`
- `XIAOLI_GO_LLM_MODEL`: default comes from `SILICONFLOW_LLM_MODEL`
- `XIAOLI_GO_VLLM_URL`: OpenAI-compatible vision chat endpoint; default `https://api.siliconflow.cn/v1/chat/completions`
- `XIAOLI_GO_VLLM_MODEL`: default comes from `SILICONFLOW_VLLM_MODEL`
- `XIAOLI_GO_TTS_RESPONSE_FORMAT`: default `opus`; keep this as Ogg Opus for board playback
- `GROQ_API_KEY`: used only if switching back to `GroqASR`
- `OPENAI_API_KEY`: used only if switching back to `OpenaiASR`
- `ZHIPU_API_KEY`: used only if switching back to `ChatGLMLLM` / `ChatGLMVLLM`
- `DASHSCOPE_API_KEY`: used if switching to `AliLLM` / `QwenVLVLLM`

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
