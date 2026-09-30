# 小李双设备构建与烧录

共用业务代码，分别生成固件。C3 与 S3 的二进制文件不能互换。

| 配置 | 设备 | Flash | 构建目录 |
|---|---|---|---|
| `s3` | 原 bread-compact-wifi-s3cam | 16MB、OPI PSRAM | `xiaozhi-esp32/build/s3/` |
| `c3` | Trae Card C3、ES8311、ST7789 | 8MB | `xiaozhi-esp32/build/c3/` |

## 只编译

```sh
./flash.sh --profile c3 --build-only
./flash.sh --profile s3 --build-only
```

默认配置位于 `xiaozhi-esp32/profiles/*.defaults`。
S3 配置以适配前的本机 sdkconfig 为基准，保留摄像头、屏幕、音频引脚和自定义资源。
生成的 sdkconfig 与 CMake 缓存分开存放，不改写根目录的 `xiaozhi-esp32/sdkconfig`。
ESP-IDF 的 managed_components 与依赖锁文件仍为共享资源，因此构建脚本串行执行。

修改 defaults 后，如构建目录已有 sdkconfig，需同步修改该配置或保存旧配置后移走
对应构建目录的 sdkconfig，再运行构建。defaults 不会覆盖已生成配置中的用户选择。

S3 还需要已有的 `main/boards/bread-compact-wifi-s3cam/assets.bin`；缺失时会报错。

## 校验与烧录

```sh
# 只读取芯片信息并校验编译产物，不写入设备
./flash.sh --profile c3 --port /dev/cu.usbmodem14101 --dry-run

# 自动识别 C3/S3，选择对应编译产物
./flash.sh --port /dev/cu.usbmodem14101

# 先构建匹配设备的固件，再烧录
./flash.sh --build --port /dev/cu.usbmodem14101
```

自动识别仅区分本项目的两种芯片配置，不会识别任意开发板的音频接线。
有多个 USB 串口时必须指定 `--port`。脚本校验芯片、Flash 容量、实际镜像头、
文件大小与烧录范围，读取 ESP-IDF 的 `flasher_args.json` 获取分区地址和资源文件。
不再使用写死的 S3 地址。旧 `build/` 目录中的产物不会自动用于烧录。

写入前提示确认，随后完整备份设备到 `backups/`，生成 SHA256，再执行烧录。
明确需要无人值守执行时可加 `--yes`。新程序会替换原程序，原厂应用与小李固件的
分区布局不同；备份保留原始完整内容，但不保证复用原厂配网信息。

## 原固件恢复

仅在确认需要恢复到原厂程序时执行，并使用对应设备的备份：

```sh
.espressif/python_env/idf5.5_py3.13_env/bin/python -m esptool \
  --chip esp32c3 --port /dev/cu.usbmodem14101 \
  write_flash 0 backups/c3-4c11ae3250c8/original-20260929.bin
```

## 验证范围

编译通过与镜像校验不能代替实机验证。需分别检查：启动无重启循环、屏幕、
按键、配网、服务端连接、唤醒、录音和播放。C3 原厂硬件分析依据见
`xiaozhi-esp32/main/boards/xiaoli-trae-c3/README.md`。
S3 保持原板级实现，仍需连接原设备完成运行回归。
