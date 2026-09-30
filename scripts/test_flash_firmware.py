import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location('flash_firmware', Path(__file__).with_name('flash_firmware.py'))
flash = importlib.util.module_from_spec(spec)
spec.loader.exec_module(flash)


class FlashSafetyTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.build = Path(self.tmp.name)
        image = bytearray(64)
        image[0] = 0xe9
        image[12] = 5
        for name in ('app.bin', 'boot.bin'):
            (self.build / name).write_bytes(image)
        self.manifest = {
            'extra_esptool_args': {'chip': 'esp32c3'},
            'flash_settings': {'flash_size': '8MB'},
            'flash_files': {'0x0': 'boot.bin', '0x20000': 'app.bin'},
            'bootloader': {'file': 'boot.bin'}, 'app': {'file': 'app.bin'},
        }

    def validate(self, chip='esp32c3', size=8):
        (self.build / 'flasher_args.json').write_text(json.dumps(self.manifest))
        return flash.validate_manifest(self.build, chip, size)

    def test_valid_c3(self):
        self.assertEqual(len(self.validate()[1]), 2)

    def test_reject_s3_device(self):
        with self.assertRaises(ValueError):
            self.validate('esp32s3', 16)

    def test_reject_flash_capacity(self):
        with self.assertRaises(ValueError):
            self.validate(size=4)

    def test_reject_mixed_actual_image(self):
        image = bytearray((self.build / 'app.bin').read_bytes())
        image[12] = 9
        (self.build / 'app.bin').write_bytes(image)
        with self.assertRaises(ValueError):
            self.validate()

    def test_reject_overlapping_images(self):
        self.manifest['flash_files'] = {'0x0': 'boot.bin', '0x10': 'app.bin'}
        with self.assertRaises(ValueError):
            self.validate()

    def test_reject_image_outside_flash(self):
        self.manifest['flash_files']['0x800000'] = self.manifest['flash_files'].pop('0x20000')
        with self.assertRaises(ValueError):
            self.validate()

    def test_probe_formats_and_unknown(self):
        for text in ('Chip is ESP32-C3 (QFN32)\nDetected flash size: 8MB',
                     'Chip type: ESP32-C3 (QFN32)\nDetected flash size: 8MB'):
            self.assertEqual(flash.parse_device(text), ('esp32c3', 8))
        with self.assertRaises(ValueError):
            flash.parse_device('Chip type: ESP32-C6\nDetected flash size: 8MB')


if __name__ == '__main__':
    unittest.main()
