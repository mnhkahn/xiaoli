#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")" && pwd)"
PYTHON="$ROOT/.espressif/python_env/idf5.5_py3.13_env/bin/python"
if [ ! -x "$PYTHON" ]; then
    echo "缺少项目 ESP-IDF Python 环境：$PYTHON" >&2
    exit 1
fi
exec "$PYTHON" "$ROOT/scripts/watch_c3.py" "$@"
