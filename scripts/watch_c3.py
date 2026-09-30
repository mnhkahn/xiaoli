#!/usr/bin/env python3
"""Merge the C3 serial console and Fly logs into a live interaction timeline."""

from __future__ import annotations

import argparse
from datetime import datetime, timedelta, timezone
import json
import os
from queue import Empty, Queue
import re
import shutil
import subprocess
import sys
from threading import Event, Thread
import time


DEFAULT_MAC = "4c:11:ae:32:50:c8"
DEFAULT_APP = "xiaoli-server"
ANSI = re.compile(r"\x1b\[[0-9;]*m")
FLY_TIME = re.compile(r"^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d+)?Z)\s")


def fly_timestamp(line: str) -> datetime | None:
    match = FLY_TIME.match(ANSI.sub("", line))
    if not match:
        return None
    try:
        return datetime.fromisoformat(match.group(1).replace("Z", "+00:00"))
    except ValueError:
        return None


def after_marker(line: str, marker: str) -> str:
    return line.split(marker, 1)[1].strip()


def server_event(line: str, mac: str) -> str | None:
    """Summarize a server log line known to belong to the selected device."""
    if mac.lower() not in line.lower():
        return None
    if "ws text from " in line:
        payload = after_marker(line, "ws text from ")
        payload = payload.split(": ", 1)[1] if ": " in payload else ""
        try:
            message = json.loads(payload)
        except json.JSONDecodeError:
            return "收到设备 JSON（解析失败）"
        kind = message.get("type")
        if kind == "listen":
            state = message.get("state", "?")
            mode = message.get("mode")
            return f"设备 → 云端  listen/{state}" + (f" mode={mode}" if mode else "")
        if kind == "hello":
            return "设备 → 云端  hello 握手"
        if kind == "abort":
            return "设备 → 云端  abort 中断播报"
        if kind == "mcp":
            payload = message.get("payload") or {}
            method = payload.get("method") if isinstance(payload, dict) else None
            return "设备 → 云端  MCP" + (f" {method}" if method else " 响应")
        return f"设备 → 云端  {kind or '未知 JSON'}"
    for marker, label in (
        ("device connected:", "WebSocket 已连接"),
        ("device disconnected:", "WebSocket 已断开"),
        ("listen start ", "开始收音"),
        ("auto-stop from ", "自动结束收音"),
        ("listen stop from ", "结束收音"),
        ("audio recv from ", "收到麦克风音频"),
        ("voice turn ASR ok for ", "ASR 识别成功"),
        ("voice turn ASR failed for ", "ASR 识别失败"),
        ("voice model.start ", "语音模型请求开始"),
        ("voice model.end ", "语音模型请求结束"),
        ("voice model.error ", "语音模型请求失败"),
        ("voice answer rejected for ", "语音回答已拦截，准备重试"),
        ("voice answer retry rejected for ", "语音回答重试仍异常"),
        ("voice turn first sentence for ", "首句文本就绪"),
        ("voice turn audio frame for ", "首个音频帧已发送"),
        ("voice turn LLM answer for ", "生成回答文本"),
        ("tts synth ok for ", "TTS 合成完成"),
        ("tts synth failed for ", "TTS 合成失败"),
        ("tts stream start for ", "开始发送语音帧"),
        ("tts stream done for ", "语音帧发送完成"),
        ("tts stream send failed for ", "语音帧发送失败"),
    ):
        if marker in line:
            detail = after_marker(line, marker)
            if marker.startswith("voice model."):
                return f"{label}  {detail}"
            detail = detail.split(": ", 1)[1] if ": " in detail else ""
            return f"{label}" + (f"  {detail}" if detail else "")
    return None


def device_event(line: str) -> str | None:
    for marker, label in (
        ("PlaybackStats:", "播放统计"),
        ("Wake word detected:", "识别唤醒词"),
        ("ADC button ", "按键"),
        ("Volume overlay:", "屏幕音量条"),
        ("Abort speaking", "请求中断播报"),
        ("Websocket disconnected", "WebSocket 已断开"),
        ("Session ID:", "WebSocket 会话建立"),
        ("Decoded OK", "音频解码完成"),
        ("AudioOutputTask", "扬声器输出"),
    ):
        if marker in line:
            return label + "  " + after_marker(line, marker)
    if "<< " in line:
        return "收到回答文本  " + after_marker(line, "<< ")
    if ">> " in line:
        return "收到 ASR 文本  " + after_marker(line, ">> ")
    return None


def find_serial_port(explicit: str | None) -> str | None:
    if explicit:
        return explicit if os.path.exists(explicit) else None
    from serial.tools import list_ports

    ports = list(list_ports.comports())
    esp = [p.device for p in ports if p.vid == 0x303A and p.device.startswith("/dev/cu.")]
    if len(esp) == 1:
        return esp[0]
    usb = [p.device for p in ports if p.device.startswith(("/dev/cu.usbmodem", "/dev/cu.usbserial"))]
    return usb[0] if len(usb) == 1 else None


def serial_worker(events: Queue, stop: Event, port: str | None) -> None:
    import serial

    previous = object()
    while not stop.is_set():
        selected = find_serial_port(port)
        if not selected:
            if previous is not None:
                events.put(("状态", "等待 C3 USB 串口；如有多个设备，请用 --port 指定"))
                previous = None
            stop.wait(2)
            continue
        try:
            with serial.Serial(selected, 115200, timeout=1) as connection:
                events.put(("状态", f"C3 串口已连接：{selected}"))
                previous = selected
                while not stop.is_set():
                    raw = connection.readline()
                    if raw:
                        events.put(("C3", raw.decode("utf-8", errors="replace").strip()))
        except (OSError, serial.SerialException) as exc:
            events.put(("状态", f"C3 串口断开：{exc}"))
            previous = object()
            stop.wait(2)


def fly_worker(events: Queue, stop: Event, app: str) -> None:
    while not stop.is_set():
        try:
            process = subprocess.Popen(
                ["flyctl", "logs", "-a", app], stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT, text=True, errors="replace", bufsize=1,
            )
        except OSError as exc:
            events.put(("状态", f"无法启动 flyctl：{exc}"))
            stop.wait(10)
            continue
        events.put(("状态", f"Fly 日志已连接：{app}"))
        finished = Event()

        def terminate_when_stopped() -> None:
            while not finished.is_set():
                if stop.wait(0.5):
                    if process.poll() is None:
                        process.terminate()
                    return

        Thread(target=terminate_when_stopped, daemon=True).start()
        try:
            assert process.stdout is not None
            for line in process.stdout:
                if stop.is_set():
                    break
                events.put(("Fly", line.rstrip()))
        finally:
            finished.set()
            if process.poll() is None:
                process.terminate()
            try:
                process.wait(timeout=2)
            except subprocess.TimeoutExpired:
                process.kill()
        if not stop.is_set():
            events.put(("状态", "Fly 日志连接结束，10 秒后重试"))
            stop.wait(10)


def run() -> int:
    parser = argparse.ArgumentParser(description="实时汇总 C3 串口与 Fly 对话日志")
    parser.add_argument("--port", help="指定 C3 串口，例如 /dev/cu.usbmodem14101")
    parser.add_argument("--mac", default=DEFAULT_MAC, help="设备 MAC，用于过滤 Fly 日志")
    parser.add_argument("--app", default=DEFAULT_APP, help="Fly 应用名")
    parser.add_argument("--raw", action="store_true", help="显示所有 C3 串口行及该设备的原始 Fly 日志")
    parser.add_argument("--history", action="store_true", help="同时显示 Fly 启动时回放的历史缓冲日志")
    args = parser.parse_args()

    if shutil.which("flyctl") is None:
        parser.error("找不到 flyctl，请先安装并登录")
    try:
        import serial  # noqa: F401
    except ImportError:
        parser.error("缺少 pyserial，请用仓库根目录的 ./watch-c3.sh 启动")

    events: Queue = Queue()
    stop = Event()
    started = datetime.now(timezone.utc) - timedelta(seconds=5)
    workers = [
        Thread(target=serial_worker, args=(events, stop, args.port), daemon=True),
        Thread(target=fly_worker, args=(events, stop, args.app), daemon=True),
    ]
    for worker in workers:
        worker.start()
    print(f"正在监听 C3 {args.mac}。时间为本机收到日志的时间；Ctrl-C 退出。", flush=True)
    try:
        while True:
            try:
                source, raw = events.get(timeout=0.5)
            except Empty:
                continue
            line = ANSI.sub("", raw)
            if source == "Fly":
                source_time = fly_timestamp(line)
                if not args.history and source_time and source_time < started:
                    continue
                event = server_event(line, args.mac)
                if args.raw and args.mac.lower() in line.lower():
                    event = line
            elif source == "C3":
                event = line if args.raw else device_event(line)
            else:
                event = line
            if event:
                now = datetime.now().astimezone().strftime("%H:%M:%S")
                print(f"{now} [{source}] {event}", flush=True)
    except KeyboardInterrupt:
        print("\n已停止监听。", flush=True)
    finally:
        stop.set()
        for worker in workers:
            worker.join(timeout=3)
    return 0


if __name__ == "__main__":
    sys.exit(run())
