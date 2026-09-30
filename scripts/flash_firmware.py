#!/usr/bin/env python3
"""Build/flash Xiaoli profiles using ESP-IDF's generated flash manifest."""
import argparse
from datetime import datetime
import hashlib
import json
from pathlib import Path
import re
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
PROFILES = {'s3': ('esp32s3', 16), 'c3': ('esp32c3', 8)}


def run(command, **kwargs):
    return subprocess.run([str(arg) for arg in command], check=True, **kwargs)


def parse_device(output):
    chip = re.search(r'(?:Chip (?:is|type:)\s*|Detecting chip type\.\.\.\s*)(ESP32-[SC]3)', output)
    size = re.search(r'Detected flash size:\s*(\d+)MB', output, re.I)
    if not chip or not size:
        raise ValueError('Cannot identify supported chip and flash size; refusing to write.')
    return chip[1].lower().replace('-', ''), int(size[1])


def validate_manifest(build, chip, flash_mb):
    manifest = json.loads((build / 'flasher_args.json').read_text())
    if manifest['extra_esptool_args']['chip'] != chip:
        raise ValueError('Build target does not match connected chip.')
    configured = manifest['flash_settings']['flash_size']
    if configured != f'{flash_mb}MB':
        raise ValueError(f'Build flash size {configured} does not match {flash_mb}MB device.')
    files = sorted((int(offset, 0), (build / name).resolve())
                   for offset, name in manifest['flash_files'].items())
    if not files or 'app' not in manifest or 'bootloader' not in manifest:
        raise ValueError('Incomplete flash manifest.')
    previous_end = 0
    for offset, path in files:
        size = path.stat().st_size
        if not size or offset < previous_end or offset + size > flash_mb * 1024 * 1024:
            raise ValueError(f'Empty, overlapping or out-of-flash image: {path}')
        previous_end = offset + size
    # Validate actual image headers too, so a stale/mixed manifest cannot hide a wrong chip.
    chip_id = {'esp32s3': 9, 'esp32c3': 5}[chip]
    for part in ('app', 'bootloader'):
        image = (build / manifest[part]['file']).read_bytes()
        if len(image) < 24 or image[0] != 0xe9 or int.from_bytes(image[12:14], 'little') != chip_id:
            raise ValueError(f'{part} image header does not match {chip}.')
    return manifest, files


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--profile', choices=['auto', 's3', 'c3'], default='auto')
    parser.add_argument('-p', '--port')
    parser.add_argument('-b', '--build', action='store_true')
    parser.add_argument('--build-only', action='store_true')
    parser.add_argument('--dry-run', action='store_true', help='Probe and validate, without backup or flash')
    parser.add_argument('-y', '--yes', action='store_true', help='Flash after validation and backup without prompt')
    args = parser.parse_args()
    if args.build_only:
        if args.profile == 'auto':
            parser.error('--build-only requires --profile s3 or c3')
        run([ROOT / 'scripts/build-firmware.sh', args.profile])
        return
    from serial.tools import list_ports
    port = args.port
    if not port:
        ports = [p.device for p in list_ports.comports() if p.vid is not None]
        if len(ports) != 1:
            raise ValueError(f'Choose --port explicitly. USB serial ports: {ports}')
        port = ports[0]
    # Use the interpreter chosen by flash.sh; do not execute relocated venv shebangs.
    tool = [sys.executable, '-m', 'esptool']
    result = run(tool + ['--port', port, '--no-stub', 'flash_id'], capture_output=True, text=True)
    print(result.stdout)
    chip, flash_mb = parse_device(result.stdout)
    detected = next(p for p, spec in PROFILES.items() if spec[0] == chip)
    profile = detected if args.profile == 'auto' else args.profile
    if PROFILES[profile] != (chip, flash_mb):
        raise ValueError(f'Profile {profile} does not match {chip}, {flash_mb}MB.')
    if args.build:
        run([ROOT / 'scripts/build-firmware.sh', profile])
    build = ROOT / 'xiaozhi-esp32/build' / profile
    manifest, files = validate_manifest(build, chip, flash_mb)
    print(f'Profile: {profile}, chip: {chip}, flash: {flash_mb}MB, port: {port}')
    for offset, path in files:
        print(f'  {offset:#010x} {path.name} ({path.stat().st_size} bytes)')
    if args.dry_run:
        print('Validation passed; no flash was written.')
        return
    if not args.yes and input('Back up device and replace its firmware? [y/N] ').strip().lower() != 'y':
        print('Cancelled.')
        return
    backup_dir = ROOT / 'backups' / f'{chip}-{datetime.now():%Y%m%d-%H%M%S-%f}'
    backup_dir.mkdir(parents=True, exist_ok=False)
    backup = backup_dir / 'original.bin'
    run(tool + ['--chip', chip, '--port', port, '--baud', '460800',
                'read_flash', '0', str(flash_mb * 1024 * 1024), backup])
    if backup.stat().st_size != flash_mb * 1024 * 1024:
        raise ValueError('Backup size mismatch; refusing to write.')
    (backup_dir / 'sha256.txt').write_text(hashlib.sha256(backup.read_bytes()).hexdigest() + '  original.bin\n')
    command = tool + ['--chip', chip, '--port', port, '--baud', '460800',
                      '--before', 'default_reset', '--after', 'hard_reset',
                      'write_flash'] + manifest['write_flash_args']
    for offset, path in files:
        command.extend([hex(offset), str(path)])
    run(command)
    print(f'Flash complete. Original firmware: {backup}')


if __name__ == '__main__':
    try:
        main()
    except subprocess.CalledProcessError as exc:
        if exc.stdout:
            print(exc.stdout, file=sys.stderr)
        if exc.stderr:
            print(exc.stderr, file=sys.stderr)
        sys.exit(f'Command failed (exit {exc.returncode}); no further steps were run.')
    except (ValueError, KeyError, OSError) as exc:
        sys.exit(f'Error: {exc}')
