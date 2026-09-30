#!/bin/bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PROFILE="${1:-s3}"
case "$PROFILE" in
    s3) TARGET=esp32s3 ;;
    c3) TARGET=esp32c3 ;;
    *) echo "Usage: $0 [s3|c3] [idf action, default: build]" >&2; exit 2 ;;
esac
ACTION="${2:-build}"
case "$ACTION" in
    build|reconfigure|menuconfig|size) ;;
    *) echo "Supported actions: build, reconfigure, menuconfig, size" >&2; exit 2 ;;
esac
PROJECT="$ROOT/xiaozhi-esp32"
BUILD="$PROJECT/build/$PROFILE"
export IDF_TOOLS_PATH="$ROOT/.espressif"
export IDF_PYTHON_ENV_PATH="$IDF_TOOLS_PATH/python_env/idf5.5_py3.13_env"
# ESP-IDF shares managed_components and dependencies.lock between profiles.
# Serialize builds across processes while keeping output and sdkconfig separate.
if [ "${XIAOLI_BUILD_LOCKED:-}" != "1" ]; then
    exec "$IDF_PYTHON_ENV_PATH/bin/python" - "$0" "$PROFILE" "$ACTION" "$PROJECT" <<'PYLOCK'
import fcntl, os, pathlib, subprocess, sys
script, profile, action, project = sys.argv[1:]
lock_path = pathlib.Path(project) / 'build' / '.xiaoli-build.lock'
lock_path.parent.mkdir(parents=True, exist_ok=True)
with lock_path.open('a') as lock:
    fcntl.flock(lock, fcntl.LOCK_EX)
    env = dict(os.environ, XIAOLI_BUILD_LOCKED='1')
    sys.exit(subprocess.call(['bash', script, profile, action], env=env))
PYLOCK
fi
export IDF_PATH="$ROOT/esp-idf"
# The project may have moved; invoke Python explicitly, not old venv shebangs.
export PATH="$IDF_PYTHON_ENV_PATH/bin:$PATH"
source "$IDF_PATH/export.sh" >/dev/null
mkdir -p "$BUILD"
cd "$PROJECT"
# Separate generated sdkconfig/cache; never run set-target/fullclean on the S3 project.
"$IDF_PYTHON_ENV_PATH/bin/python" "$IDF_PATH/tools/idf.py" \
    -B "$BUILD" -D "IDF_TARGET=$TARGET" \
    -D "SDKCONFIG=$BUILD/sdkconfig" \
    -D "SDKCONFIG_DEFAULTS=$PROJECT/sdkconfig.defaults;$PROJECT/profiles/$PROFILE.defaults" \
    "$ACTION"
