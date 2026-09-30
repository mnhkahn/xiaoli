#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")" && pwd)"
PYTHON="$ROOT/.espressif/python_env/idf5.5_py3.13_env/bin/python"
if [ ! -x "$PYTHON" ]; then
    echo "Missing ESP-IDF Python environment: $PYTHON" >&2
    exit 1
fi
exec "$PYTHON" "$ROOT/scripts/flash_firmware.py" "$@"
