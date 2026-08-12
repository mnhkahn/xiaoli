package admin

import (
	"html"
)

func dashboardHTML(user map[string]any) string {
	userLabel := html.EscapeString(firstNonEmpty(user, "email", "name", "sub"))
	return `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>小李设备后台</title>
  <style>
    * { box-sizing: border-box; }
    body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #f6f7f9; color: #17202a; overflow-x: hidden; }
    header { display: flex; align-items: center; justify-content: space-between; padding: 14px 20px; border-bottom: 1px solid #d9dee7; background: #fff; }
    h1 { margin: 0; font-size: 18px; font-weight: 650; }
    h2 { margin: 0 0 12px; font-size: 15px; }
    main { max-width: 1360px; margin: 0 auto; padding: 18px; display: grid; gap: 16px; }
    section { background: #fff; border: 1px solid #d9dee7; border-radius: 8px; padding: 14px; }
    .tool-grid { display: grid; gap: 16px; grid-template-columns: minmax(280px, 380px) minmax(0, 1fr); align-items: start; }
    button, select, input, textarea { font: inherit; }
    button { border: 1px solid #d9dee7; background: #fff; border-radius: 6px; padding: 8px 10px; cursor: pointer; min-height: 36px; }
    button.primary { background: #0f766e; border-color: #0f766e; color: #fff; }
    button:disabled { opacity: .56; cursor: wait; }
    button[aria-busy="true"] { position: relative; }
    button[aria-busy="true"]::after { content: ""; display: inline-block; width: 10px; height: 10px; margin-left: 8px; border: 2px solid currentColor; border-right-color: transparent; border-radius: 999px; animation: spin .7s linear infinite; vertical-align: -1px; }
    select, input, textarea { width: 100%; border: 1px solid #d9dee7; border-radius: 6px; padding: 8px; background: #fff; }
    textarea { min-height: 180px; resize: vertical; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 13px; }
    pre { margin: 0; white-space: pre-wrap; word-break: break-word; background: #101828; color: #e6edf3; border-radius: 6px; padding: 12px; min-height: 320px; max-height: 70vh; overflow: auto; }
    .row { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
    .stack { display: grid; gap: 10px; }
    .device-row { display: grid; gap: 8px; grid-template-columns: auto minmax(260px, 1fr) auto; align-items: center; }
    .pairing-card { margin-top: 12px; display: none; gap: 12px; grid-template-columns: 180px minmax(0, 1fr); align-items: start; }
    .pairing-card.visible { display: grid; }
    .pairing-card img { width: 180px; height: 180px; border: 1px solid #d9dee7; border-radius: 6px; image-rendering: pixelated; }
    .pairing-card textarea { min-height: 110px; }
    .muted { color: #667085; font-size: 13px; }
    .tabs { display: flex; gap: 6px; border-bottom: 1px solid #d9dee7; margin-bottom: 14px; overflow-x: auto; }
    .tab-button { border-bottom-left-radius: 0; border-bottom-right-radius: 0; margin-bottom: -1px; white-space: nowrap; }
    .tab-button.active { border-color: #0f766e; border-bottom-color: #fff; color: #0f766e; font-weight: 650; }
    .tab-panel { display: grid; gap: 12px; }
    .tab-panel[hidden] { display: none; }
    .preview { display: grid; gap: 10px; }
    img.preview-image { max-width: min(100%, 640px); border: 1px solid #d9dee7; border-radius: 6px; background: #f2f4f7; }
    .video-controls { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
    .video-controls select { width: auto; min-width: 170px; }
    .stream-viewer { position: relative; display: grid; place-items: center; min-height: 440px; border: 1px solid #d9dee7; border-radius: 8px; background: #0b1220; overflow: hidden; }
    .stream-viewer img { display: none; width: 100%; max-height: 72vh; object-fit: contain; }
    .stream-viewer.has-frame img { display: block; }
    .stream-viewer.has-frame .stream-placeholder { display: none; }
    .stream-placeholder { color: #cbd5e1; font-size: 14px; }
    .schedule-list { display: grid; gap: 10px; }
    .schedule-item { border: 1px solid #d9dee7; border-radius: 8px; padding: 12px; display: grid; gap: 8px; }
    .schedule-title { display: flex; gap: 8px; align-items: center; justify-content: space-between; }
    .pill { border-radius: 999px; padding: 2px 8px; font-size: 12px; background: #eef2f6; color: #667085; }
    .pill.ok { background: #dcfae6; color: #067647; }
    @keyframes spin { to { transform: rotate(360deg); } }
    @media (max-width: 820px) { .tool-grid, .device-row, .pairing-card { grid-template-columns: 1fr; } .video-controls select { min-width: auto; width: 100%; } }
    @media (max-width: 640px) { main { padding: 12px; } section { padding: 10px; } .stream-viewer { min-height: 240px; } pre { min-height: 200px; } .tab-button { font-size: 13px; padding: 6px 8px; } }
  </style>
</head>
<body>
  <header>
    <h1>小李设备后台</h1>
    <div class="row"><a href="/admin/memory">记忆查看</a><span class="muted">` + userLabel + `</span><a href="/admin/logout">退出</a></div>
  </header>
  <main>
    <section id="deviceBar">
      <div class="device-row">
        <label class="muted" for="device">设备列表</label>
        <select id="device"></select>
        <div class="row"><button id="refresh">刷新</button><button id="createPairing" class="primary">添加学习平板</button></div>
      </div>
      <div id="pairingCard" class="pairing-card">
        <img id="pairingQR" alt="学习平板配对二维码">
        <div class="stack"><strong>用学习平板的“扫描二维码”完成绑定</strong><span id="pairingExpiry" class="muted"></span><textarea id="pairingPayload" readonly aria-label="配对二维码内容"></textarea></div>
      </div>
    </section>
    <section>
      <div class="tabs">
        <button class="tab-button active" data-tab-target="toolsTab" type="button">MCP 工具</button>
        <button class="tab-button" data-tab-target="videoTab" type="button">视频播放</button>
        <button class="tab-button" data-tab-target="audioTab" type="button">语音文本发送</button>
        <button class="tab-button" data-tab-target="scheduleTab" type="button">定时任务</button>
      </div>
      <div id="toolsTab" class="tab-panel">
        <div class="tool-grid">
          <div class="stack">
            <h2>MCP 工具</h2>
            <select id="tool"></select>
            <textarea id="args">{}</textarea>
            <button id="call" class="primary">调用</button>
          </div>
          <div class="stack">
            <h2>结果</h2>
            <div id="preview" class="preview"></div>
            <pre id="output">加载中...</pre>
          </div>
        </div>
      </div>
      <div id="videoTab" class="tab-panel" hidden>
        <div class="video-controls">
          <select id="snapshotResolution" aria-label="拍照清晰度">
            <option value="qvga">快速 320x240</option>
            <option value="vga" selected>标准 640x480</option>
            <option value="svga">清晰 800x600</option>
            <option value="xga">细节 1024x768</option>
            <option value="uxga">最高 1600x1200</option>
            <option value="legacy_vga">旧版 640x480</option>
          </select>
          <button id="snapshot">拍照</button>
          <select id="streamResolution" aria-label="视频清晰度">
            <option value="qqvga" selected>极速 160x120</option>
            <option value="qvga">低清 320x240</option>
            <option value="vga">标准 640x480</option>
            <option value="svga">清晰 800x600</option>
          </select>
          <button id="streamStart" class="primary">开始视频流</button>
          <button id="streamStop">停止视频流</button>
          <span id="streamStatus" class="muted">等待开始</span>
        </div>
        <div id="streamViewer" class="stream-viewer">
          <img id="streamImage" alt="视频画面">
          <div id="streamPlaceholder" class="stream-placeholder">点击开始视频流后，画面会显示在这里</div>
        </div>
      </div>
      <div id="audioTab" class="tab-panel" hidden>
        <label class="muted" for="speakText">要发送给设备播放的文字</label>
        <textarea id="speakText" placeholder="输入要播放的文字"></textarea>
        <div class="row">
          <button id="speak" class="primary">发送语音文本</button>
          <button id="speakStop">停止语音</button>
        </div>
        <pre id="speakOutput">等待发送...</pre>
      </div>
      <div id="scheduleTab" class="tab-panel" hidden>
        <div class="row">
          <h2>定时任务</h2>
          <button id="refreshSchedules">刷新</button>
        </div>
        <div id="schedules" class="schedule-list"></div>
      </div>
    </section>
  </main>
  <script>
    const $ = (id) => document.getElementById(id);
    let channels = [];
    let devices = [];
    let tools = [];
    let streamSocket = null;
    let directStreamURL = "";

    async function api(url, options = {}) {
      const res = await fetch(url, { credentials: "same-origin", ...options });
      if (!res.ok) throw new Error(await res.text());
      return await res.json();
    }

    async function withBusy(button, busyText, action) {
      if (!button || button.disabled) return;
      const originalText = button.textContent;
      button.disabled = true;
      button.setAttribute("aria-busy", "true");
      button.textContent = busyText;
      try {
        return await action();
      } finally {
        button.disabled = false;
        button.removeAttribute("aria-busy");
        button.textContent = originalText;
      }
    }

    function selectedChannel() { return $("device").value || ""; }
    function selectedDevice() {
      const option = $("device").selectedOptions[0];
      return (option && option.dataset.deviceId) || "";
    }
    function show(value) { $("output").textContent = typeof value === "string" ? value : JSON.stringify(value, null, 2); }
    function showSpeak(value) { $("speakOutput").textContent = typeof value === "string" ? value : JSON.stringify(value, null, 2); }
    function setStreamStatus(text) { $("streamStatus").textContent = text; }

    function renderPreview(preview) {
      $("preview").innerHTML = "";
      if (!preview) return;
      for (const src of preview.images || []) {
        const img = document.createElement("img");
        img.className = "preview-image";
        img.src = src;
        $("preview").appendChild(img);
      }
      if (preview.text) {
        const p = document.createElement("p");
        p.textContent = preview.text;
        $("preview").appendChild(p);
      }
    }

    function renderStreamImage(src) {
      if (!src) return;
      $("streamImage").src = src;
      $("streamViewer").classList.add("has-frame");
    }

    function renderDirectStream(url) {
      if (!url) return;
      directStreamURL = url;
      const separator = url.includes("?") ? "&" : "?";
      $("streamImage").onerror = async () => {
        if (!directStreamURL) return;
        directStreamURL = "";
        setStreamStatus("退回后台中转流");
        try {
          await startRelayedStream(selectedDevice());
        } catch (err) {
          setStreamStatus(String(err));
        }
      };
      $("streamImage").src = url + separator + "ts=" + Date.now();
      $("streamViewer").classList.add("has-frame");
      setStreamStatus("局域网直连播放中");
    }

    async function loadDevices() {
      const data = await api("/admin/api/channels");
      channels = data.channels || [];
      devices = channels.filter(channel => channel.type === "esp32" && channel.device_id).map(channel => channel.raw && channel.raw.device ? channel.raw.device : { device_id: channel.device_id });
      if (!channels.length) {
        $("device").innerHTML = "<option value=\"\">当前没有可用通道</option>";
        $("tool").innerHTML = "";
        show("当前没有可用通道");
        return;
      }
      const current = selectedChannel();
      $("device").innerHTML = channels.map(channel => {
        const caps = channel.capabilities || {};
        const deviceID = channel.device_id || "";
        const label = (channel.display_name || channel.id) + " [" + channel.type + "] " + (caps.tools ? "ready" : channel.status || "");
        return "<option value=\"" + channel.id + "\" data-device-id=\"" + deviceID + "\">" + label + "</option>";
      }).join("");
      if (current && channels.some(channel => channel.id === current)) $("device").value = current;
      await loadTools();
      show(data);
    }
    async function loadTools() {
      const id = selectedDevice();
      if (!id) {
        tools = [];
        $("tool").innerHTML = "";
        $("args").value = "{}";
        return;
      }
      const data = await api("/admin/api/tools?device_id=" + encodeURIComponent(id));
      tools = data.tools || [];
      $("tool").innerHTML = tools.map(t => {
        const name = ((t.function || {}).name || "");
        return "<option value=\"" + name + "\">" + name + "</option>";
      }).join("");
      updateArgsFromTool();
    }

    function defaultValueForSchema(key, schema) {
      schema = schema || {};
      key = String(key || "").toLowerCase();
      if (key === "question") return "请描述这张图片里的内容。";
      if (["query", "prompt", "text", "message", "instruction"].includes(key)) return "请帮我执行这个工具。";
      if (schema.default !== undefined) return schema.default;
      if (schema.enum && schema.enum.length) return schema.enum[0];
      if (schema.minimum !== undefined) return schema.minimum;
      if (schema.type === "integer" || schema.type === "number") return 0;
      if (schema.type === "boolean") return false;
      if (schema.type === "array") return [];
      if (schema.type === "object" && schema.properties) {
        const nested = {};
        for (const [childKey, childSchema] of Object.entries(schema.properties)) {
          nested[childKey] = defaultValueForSchema(childKey, childSchema);
        }
        return nested;
      }
      if (schema.type === "object") return {};
      return "";
    }

    function updateArgsFromTool() {
      const toolName = $("tool").value;
      const tool = tools.find(t => ((t.function || {}).name || "") === toolName);
      const params = ((tool || {}).function || {}).parameters || {};
      const props = params.properties || {};
      const required = new Set(params.required || []);
      const args = {};
      for (const [key, schema] of Object.entries(props)) {
        if (required.has(key) || schema.default !== undefined || Object.keys(props).length <= 8) {
          args[key] = defaultValueForSchema(key, schema);
        }
      }
      $("args").value = JSON.stringify(args, null, 2);
    }

    async function loadSchedules() {
      const data = await api("/admin/api/schedules");
      const schedules = data.schedules || [];
      $("schedules").innerHTML = schedules.map(item => {
        const enabled = item.enabled ? "启用" : "停用";
        const cls = item.enabled ? "pill ok" : "pill";
        return "<div class=\"schedule-item\">" +
          "<div class=\"schedule-title\"><strong>" + (item.name || item.id || "") + "</strong><span class=\"" + cls + "\">" + enabled + "</span></div>" +
          "<div class=\"muted\">" + (item.description || "") + "</div>" +
          "<div class=\"muted\">时间窗：" + (item.window || "-") + " / 时区：" + (item.timezone || "-") + " / 间隔：" + (item.interval_seconds || "-") + " 秒</div>" +
          "<div class=\"muted\">工具：" + (item.camera_tool || "-") + "</div>" +
        "</div>";
      }).join("") || "<p class=\"muted\">暂无定时任务</p>";
    }

    async function callTool(name, args = {}) {
      const data = await api("/admin/api/call", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_id: selectedDevice(), tool: name, arguments: args, timeout: timeoutForTool(name) }),
      });
      renderPreview(data.preview);
      show(data);
    }

    function timeoutForTool(name) {
      name = String(name || "").toLowerCase();
      return /camera|photo|vision|image|snapshot|拍照|摄像/.test(name) ? 120 : 30;
    }

    $("refresh").onclick = () => withBusy($("refresh"), "刷新中...", loadDevices);
    $("createPairing").onclick = () => withBusy($("createPairing"), "生成中...", async () => {
      const pairing = await api("/admin/api/device-pairings", { method: "POST" });
      $("pairingQR").src = pairing.qr_image;
      $("pairingPayload").value = JSON.stringify(pairing.qr_payload || {}, null, 2);
      $("pairingExpiry").textContent = "有效至：" + new Date(pairing.expires_at).toLocaleString();
      $("pairingCard").classList.add("visible");
    }).catch(err => show(String(err)));
    $("device").onchange = loadTools;
    $("tool").onchange = updateArgsFromTool;
    document.querySelectorAll("[data-tab-target]").forEach((button) => {
      button.addEventListener("click", () => {
        document.querySelectorAll("[data-tab-target]").forEach((item) => item.classList.remove("active"));
        document.querySelectorAll(".tab-panel").forEach((panel) => panel.hidden = true);
        button.classList.add("active");
        const panel = document.getElementById(button.dataset.tabTarget);
        if (panel) panel.hidden = false;
        if (button.dataset.tabTarget === "scheduleTab") loadSchedules().catch(err => $("schedules").textContent = String(err));
      });
    });
    $("call").onclick = () => withBusy($("call"), "调用中...", async () => {
      await callTool($("tool").value, JSON.parse($("args").value || "{}"));
    }).catch(err => show(String(err)));
    $("speak").onclick = async () => {
      await withBusy($("speak"), "发送中...", async () => {
        const data = await api("/admin/api/speak", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ device_id: selectedDevice(), text: $("speakText").value }),
        });
        showSpeak(data);
      }).catch(err => showSpeak(String(err)));
    };
    $("speakStop").onclick = async () => {
      await withBusy($("speakStop"), "停止中...", async () => {
        const data = await api("/admin/api/speak/stop", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ device_id: selectedDevice() }),
        });
        showSpeak(data);
      }).catch(err => showSpeak(String(err)));
    };
    async function startRelayedStream(id) {
      if (!id) throw new Error("没有选择在线设备");
      if (streamSocket) streamSocket.close();
      directStreamURL = "";
      const scheme = location.protocol === "https:" ? "wss:" : "ws:";
      streamSocket = new WebSocket(scheme + "//" + location.host + "/admin/ws/stream?device_id=" + encodeURIComponent(id));
      streamSocket.onopen = () => setStreamStatus("等待画面...");
      streamSocket.onmessage = (event) => {
        const data = JSON.parse(event.data);
        if (data.image) {
          $("streamImage").onerror = null;
          renderStreamImage(data.image);
          setStreamStatus("后台中转播放中");
        }
      };
      streamSocket.onerror = () => setStreamStatus("视频连接异常");
      streamSocket.onclose = () => {
        if (!directStreamURL) setStreamStatus("视频连接已关闭");
      };
      await api("/admin/api/stream/start", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_id: id, fps: 2, duration_sec: 30, resolution: $("streamResolution").value, transport: "remote" }),
      });
    }
    async function captureSnapshot() {
      const id = selectedDevice();
      if (!id) throw new Error("没有选择在线设备");
      setStreamStatus("正在拍照...");
      const data = await api("/admin/api/snapshot", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ device_id: id, resolution: $("snapshotResolution").value }),
      });
      renderPreview(data.preview);
      renderStreamImage((data.preview.images || [])[0]);
      show(data);
      setStreamStatus("拍照完成");
    }
    $("snapshot").onclick = () => withBusy($("snapshot"), "拍照中...", captureSnapshot).catch(err => {
      setStreamStatus(String(err));
      show(String(err));
    });
    $("streamStart").onclick = async () => {
      await withBusy($("streamStart"), "启动中...", async () => {
        const id = selectedDevice();
        if (!id) throw new Error("没有选择在线设备");
        if (streamSocket) streamSocket.close();
        directStreamURL = "";
        $("streamImage").onerror = null;
        $("streamViewer").classList.remove("has-frame");
        $("streamImage").src = "";
        setStreamStatus("连接局域网直连流...");
        const response = await api("/admin/api/stream/start", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ device_id: id, fps: 2, duration_sec: 30, resolution: $("streamResolution").value, transport: "lan" }),
        });
        const result = response.result || {};
        show(response);
        if (result.mjpeg_url) {
          renderDirectStream(result.mjpeg_url);
          return;
        }
        setStreamStatus("退回后台中转流");
        await startRelayedStream(id);
      }).catch(err => setStreamStatus(String(err)));
    };
    $("streamStop").onclick = async () => {
      await withBusy($("streamStop"), "停止中...", async () => {
        const id = selectedDevice();
        if (streamSocket) streamSocket.close();
        directStreamURL = "";
        $("streamImage").onerror = null;
        await api("/admin/api/stream/stop", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ device_id: id }),
        });
        setStreamStatus("已停止");
      }).catch(err => setStreamStatus(String(err)));
    };
    $("refreshSchedules").onclick = () => withBusy($("refreshSchedules"), "刷新中...", loadSchedules).catch(err => $("schedules").textContent = String(err));
    loadDevices().catch(err => show(String(err)));
  </script>
</body>
	</html>`
}

func memoryHTML(user map[string]any) string {
	userLabel := html.EscapeString(firstNonEmpty(user, "email", "name", "sub"))
	return `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>小李记忆查看</title>
  <style>
    * { box-sizing: border-box; }
    body { margin: 0; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #f6f7f9; color: #17202a; overflow-x: hidden; }
    header { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 14px 20px; border-bottom: 1px solid #d9dee7; background: #fff; }
    h1 { margin: 0; font-size: 18px; font-weight: 650; }
    h2 { margin: 0; font-size: 15px; }
    a { color: #0f766e; text-decoration: none; }
    main { max-width: 1480px; margin: 0 auto; padding: 18px; display: grid; gap: 14px; }
    section { background: #fff; border: 1px solid #d9dee7; border-radius: 8px; padding: 14px; }
    button, select, input { font: inherit; }
    button { border: 1px solid #d9dee7; background: #fff; border-radius: 6px; padding: 8px 10px; cursor: pointer; min-height: 36px; }
    button.primary { background: #0f766e; border-color: #0f766e; color: #fff; }
    select, input { width: 100%; border: 1px solid #d9dee7; border-radius: 6px; padding: 8px; background: #fff; min-height: 36px; }
    pre { margin: 0; white-space: pre-wrap; word-break: break-word; background: #101828; color: #e6edf3; border-radius: 6px; padding: 12px; min-height: 240px; max-height: 62vh; overflow: auto; }
    .row { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
    .muted { color: #667085; font-size: 13px; }
    .toolbar { display: grid; grid-template-columns: minmax(220px, 1fr) 160px auto; gap: 8px; align-items: center; }
    .memory-picker { margin-top: 12px; display: grid; gap: 10px; }
    .memory-picker .list { grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); max-height: 220px; }
    .memory-grid { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: 14px; align-items: start; }
    .panel { display: grid; gap: 10px; min-width: 0; }
    .list { display: grid; gap: 8px; max-height: 72vh; overflow: auto; padding-right: 4px; min-width: 0; }
    .item { text-align: left; display: grid; gap: 4px; border-radius: 6px; }
    .item.active { border-color: #0f766e; box-shadow: 0 0 0 1px #0f766e inset; }
    .message { width: 100%; border: 1px solid #d9dee7; border-left-width: 4px; border-radius: 6px; padding: 10px 12px; display: block; text-align: left; overflow: hidden; color: #17202a; background: #fff; -webkit-appearance: none; appearance: none; }
    .message.user { border-left-color: #2563eb; background: #eff6ff; }
    .message.assistant { border-left-color: #0f766e; background: #ecfdf5; }
    .message.tool { border-left-color: #9333ea; background: #faf5ff; }
    .message.active { outline: 2px solid #17202a; }
    .message-head { display: grid; grid-template-columns: auto minmax(0, 1fr) auto; align-items: center; width: 100%; gap: 8px; font-size: 12px; color: #667085; overflow: hidden; }
    .message-label, .message-finish { white-space: nowrap; }
    .message-preview { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: #17202a; font-size: 13px; }
    .stats { display: flex; gap: 8px; flex-wrap: wrap; }
    .pill { border-radius: 999px; padding: 2px 8px; font-size: 12px; background: #eef2f6; color: #667085; }
    .pill.ok { background: #dcfae6; color: #067647; }
    .truncate { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
    @media (max-width: 1100px) { .memory-grid { grid-template-columns: 1fr; } .toolbar { grid-template-columns: 1fr; } }
  </style>
</head>
<body>
  <header>
    <div class="row"><h1>小李记忆查看</h1><a href="/admin">返回设备后台</a></div>
    <div class="row"><span class="muted">` + userLabel + `</span><a href="/admin/logout">退出</a></div>
  </header>
  <main>
<section>
      <div class="toolbar">
        <select id="channelSelect" aria-label="选择 Channel">
          <option value="__recent__">最近会话</option>
        </select>
        <button id="refreshMemory" class="primary">刷新最近会话</button>
        <button id="browseChannels">按 Channel 浏览</button>
      </div>
      <div id="memoryStatus" class="muted" style="margin-top:10px;">加载中…</div>
      <div class="memory-picker">
        <div class="row">
          <h2>会话列表</h2>
          <span class="muted" id="sessionHint">默认显示最近活跃的会话。</span>
        </div>
        <div id="sessionList" class="list"></div>
      </div>
    </section>
    <section class="memory-grid">
      <div class="panel">
        <h2>消息时间线</h2>
        <div id="messageList" class="list"></div>
      </div>
      <div class="panel">
        <h2>会话信息</h2>
        <div id="sessionMeta" class="stats"></div>
        <pre id="messageDetail">选择一条消息查看详情。</pre>
      </div>
    </section>
  </main>
  <script>
    const $ = (id) => document.getElementById(id);
    let state = { channels: [], channelName: "", channelUser: "", sessions: [], currentSessionID: "", selectedSessionID: "", detail: null, selectedIndex: null };

    async function api(url) {
      const res = await fetch(url, { credentials: "same-origin" });
      if (!res.ok) throw new Error(await res.text());
      return await res.json();
    }

    function setStatus(text) { $("memoryStatus").textContent = text; }
    function clearNode(node) { while (node.firstChild) node.removeChild(node.firstChild); }
    function text(value) { return value === undefined || value === null ? "" : String(value); }

    async function loadRecentSessions() {
      setStatus("加载最近会话…");
      const data = await api("/admin/api/memory/recent-sessions");
      state.sessions = data.sessions || [];
      state.currentSessionID = "";
      renderSessionList();
      setStatus("最近 " + state.sessions.length + " 个会话。");
    }

    async function loadChannels() {
      setStatus("加载 Channel 列表…");
      const data = await api("/admin/api/memory/channels");
      const sel = $("channelSelect");
      clearNode(sel);
      sel.appendChild(el("option", "最近会话", "__recent__"));
      if (!data.enabled || !data.channels.length) {
        sel.value = "__recent__";
        setStatus("没有找到 Channel 数据。");
        return;
      }
      state.channels = data.channels;
      const seen = {};
      for (const ch of data.channels) {
        const key = ch.channel_name + ":" + ch.channel_user;
        if (seen[key]) continue;
        seen[key] = true;
        const opt = el("option", ch.channel_name + " — " + ch.channel_user, key);
        sel.appendChild(opt);
      }
      sel.value = "__recent__";
      sel.onchange = () => selectChannel();
      setStatus("已加载 " + data.channels.length + " 个 Channel 条目；请选择一个 Channel 查看历史会话。");
    }

    function selectChannel() {
      const val = $("channelSelect").value;
      if (!val) return;
      if (val === "__recent__") {
        $("sessionHint").textContent = "默认显示最近活跃的会话。";
        loadRecentSessions();
        return;
      }
      const parts = val.split(":");
      state.channelName = parts[0];
      state.channelUser = parts.slice(1).join(":");
      $("sessionHint").textContent = state.channelName + " / " + state.channelUser;
      loadSessions();
    }

    async function loadSessions() {
      setStatus("加载会话列表…");
      const data = await api("/admin/api/memory/sessions?channel_name=" + encodeURIComponent(state.channelName) + "&channel_user=" + encodeURIComponent(state.channelUser));
      state.sessions = data.sessions || [];
      state.currentSessionID = data.current_session_id || "";
      renderSessionList();
      setStatus("共 " + state.sessions.length + " 个会话，当前：" + (state.currentSessionID || "无"));
    }

    function el(tag, textContent, value) {
      const e = document.createElement(tag);
      if (textContent !== undefined) e.textContent = textContent;
      if (value !== undefined) e.value = value;
      return e;
    }

    function renderSessionList() {
      const list = $("sessionList");
      clearNode(list);
      if (!state.sessions.length) {
        list.appendChild(el("p", "暂无会话。", "")).className = "muted";
        return;
      }
      for (const s of state.sessions) {
        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "item" + (s.id === state.selectedSessionID ? " active" : "");
        btn.onclick = () => loadSession(s.id);

        const title = el("strong", s.title || "无标题");
        title.className = "truncate";
        const id = el("span", s.id);
        id.className = "muted truncate";
        const meta = el("span", s.model + "  " + s.count + "条  " + (s.updated_at || "").slice(0, 16));
        meta.className = "muted truncate";
        if (s.id === state.currentSessionID) {
          const tag = el("span", "当前");
          tag.className = "pill ok";
          btn.appendChild(tag);
        }
        btn.appendChild(title);
        btn.appendChild(id);
        btn.appendChild(meta);
        list.appendChild(btn);
      }
    }

    async function loadSession(sessionID) {
      state.selectedSessionID = sessionID;
      renderSessionList();
      setStatus("加载会话…");
      const data = await api("/admin/api/memory/session?id=" + encodeURIComponent(sessionID));
      state.detail = data;
      state.selectedIndex = null;
      renderDetail();
      setStatus("会话 " + sessionID + "，共 " + (data.messages || []).length + " 条消息。");
    }

    function renderDetail() {
      const list = $("messageList");
      const meta = $("sessionMeta");
      clearNode(list);
      clearNode(meta);
      $("messageDetail").textContent = "选择一条消息查看详情。";
      const detail = state.detail;
      if (!detail) return;

      const info = detail.info || {};
      for (const item of [
        info.title || "无标题",
        info.model || "",
        "共 " + (detail.message_count || 0) + " 条",
        info.channel_name + " / " + info.channel_user
      ].filter(Boolean)) {
        const pill = document.createElement("span");
        pill.className = "pill";
        pill.textContent = item;
        meta.appendChild(pill);
      }

      for (const msg of detail.messages || []) {
        const btn = document.createElement("button");
        btn.type = "button";
        btn.className = "message " + (msg.role || "");
        btn.onclick = () => selectMessage(msg);

        const head = document.createElement("span");
        head.className = "message-head";
        const ts = msg.timestamp ? new Date(msg.timestamp).toLocaleString("zh-CN", { hour12: false }) : "";
        const left = el("span", "#" + (msg.index !== undefined ? msg.index : "") + " " + (msg.role || "") + (ts ? " " + ts : ""));
        left.className = "message-label";
        const preview = el("span", msg.content || msg.reasoning_content || "(no text)");
        preview.className = "message-preview";
        const right = el("span", msg.finish_reason ? "finish: " + msg.finish_reason : "");
        right.className = "message-finish";
        head.appendChild(left);
        head.appendChild(preview);
        head.appendChild(right);
        btn.appendChild(head);
        list.appendChild(btn);
      }
      if (!(detail.messages || []).length) {
        list.appendChild(el("p", "这个会话没有消息。")).className = "muted";
      }
    }

    function selectMessage(msg) {
      state.selectedIndex = msg.index;
      document.querySelectorAll(".message").forEach(n => n.classList.remove("active"));
      for (const n of document.querySelectorAll(".message")) {
        if (n.textContent.startsWith("#" + msg.index + " ")) n.classList.add("active");
      }
      $("messageDetail").textContent = JSON.stringify(msg, null, 2);
    }

    $("channelSelect").onchange = () => selectChannel();
    $("refreshMemory").onclick = () => loadRecentSessions();
    $("browseChannels").onclick = () => loadChannels();
    loadRecentSessions();
  </script>
</body>
</html>`
}

func firstNonEmpty(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(m[key]); value != "" && value != "<nil>" {
			return value
		}
	}
	return "admin"
}
