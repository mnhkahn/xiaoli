import sys
from pathlib import Path
import unittest

sys.path.insert(0, str(Path(__file__).resolve().parent))

from watch_c3 import device_event, fly_timestamp, server_event


MAC = "4c:11:ae:32:50:c8"


class WatchC3ParsingTests(unittest.TestCase):
    def test_protocol_json_is_summarized(self):
        line = f'ws text from {MAC}: {{"type":"listen","state":"start","mode":"auto"}}'
        self.assertEqual(server_event(line, MAC), "设备 → 云端  listen/start mode=auto")
        self.assertIsNone(server_event(line, "other-device"))

    def test_voice_turn_stages_are_distinct(self):
        self.assertIn("ASR 识别成功", server_event(f'voice turn ASR ok for {MAC}: text="你好"', MAC))
        self.assertIn("已拦截", server_event(f'voice turn ASR rejected for {MAC}: reason=unsupported_language raw="こんにちは"', MAC))
        self.assertIn("语音帧发送完成", server_event(f'tts stream done for {MAC}: sent=20', MAC))

    def test_voice_model_timing_is_visible(self):
        started = server_event(f"voice model.start device={MAC} request=1 model=openrouter:free-router messages=2 tools=3", MAC)
        ended = server_event(f"voice model.end device={MAC} request=1 model=openrouter:free-router elapsedMS=850 firstTokenMS=330 chars=8 toolCalls=0", MAC)
        self.assertIn("request=1 model=openrouter:free-router", started)
        self.assertIn("elapsedMS=850 firstTokenMS=330", ended)

    def test_voice_answer_rejection_is_visible(self):
        rejected = server_event(f'voice answer rejected for {MAC}: reason=prompt_echo input="你好" output="提示词"', MAC)
        retried = server_event(f'voice answer retry rejected for {MAC}: reason=self_analysis', MAC)
        self.assertIn("准备重试", rejected)
        self.assertIn("重试仍异常", retried)

    def test_device_text_and_button(self):
        self.assertEqual(device_event('I (42) Application: << 你好'), '收到回答文本  你好')
        self.assertIn('按键', device_event('I (42) XiaoliTraeC3: ADC button 2 pressed'))
        self.assertIn('full_drop=1', device_event('I (42) AudioService: PlaybackStats: rx=20 full_drop=1'))

    def test_fly_time_ignores_ansi(self):
        value = fly_timestamp('\x1b[2m2026-09-30T02:07:16Z\x1b[0m app[x] log')
        self.assertEqual(value.isoformat(), '2026-09-30T02:07:16+00:00')


if __name__ == '__main__':
    unittest.main()
