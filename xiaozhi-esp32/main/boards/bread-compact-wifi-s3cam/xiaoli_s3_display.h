#pragma once

#include "assets.h"
#include "display/lcd_display.h"
#include "display/lvgl_display/lvgl_theme.h"

#include <array>
#include <cstring>

class XiaoliS3Display : public SpiLcdDisplay {
    static constexpr int kFaceWidth = 168;
    static constexpr int kFaceHeight = 200;
    static constexpr std::array<const char*, 24> kFaceNames = {
        "neutral", "happy", "laughing", "funny", "sad", "angry", "crying",
        "loving", "embarrassed", "surprised", "shocked", "thinking", "winking",
        "cool", "relaxed", "delicious", "kissy", "confident", "sleepy",
        "silly", "confused", "listening", "speaking", "blink"
    };
    std::array<lv_image_dsc_t, kFaceNames.size()> face_images_{};

public:
    using SpiLcdDisplay::SpiLcdDisplay;

    void SetupUI() override {
        auto* theme = LvglThemeManager::GetInstance().GetTheme("dark");
        theme->set_background_color(lv_color_hex(0x041220));
        theme->set_chat_background_color(lv_color_hex(0x041220));
        current_theme_ = theme;
        LcdDisplay::SetupUI();
        DisplayLockGuard lock(this);
        lv_obj_set_size(emoji_box_, 220, 210);
        lv_obj_align(emoji_box_, LV_ALIGN_CENTER, 0, 0);
        lv_obj_center(emoji_label_);
        lv_obj_center(emoji_image_);
    }

    void SetEmotion(const char* emotion) override {
        if (!IsSetupUICalled() || !emoji_image_ || !emotion) {
            LcdDisplay::SetEmotion(emotion ? emotion : "neutral");
            return;
        }
        if (strcmp(emotion, "microchip_ai") == 0) emotion = "neutral";
        for (size_t i = 0; i < kFaceNames.size(); ++i) {
            if (strcmp(emotion, kFaceNames[i]) != 0) continue;
            char filename[40];
            snprintf(filename, sizeof(filename), "face_%s.rgb565", emotion);
            void* data = nullptr;
            size_t size = 0;
            if (!Assets::GetInstance().GetAssetData(filename, data, size) ||
                size != kFaceWidth * kFaceHeight * 2 ||
                (reinterpret_cast<uintptr_t>(data) & 3) != 0) {
                ESP_LOGW("XiaoliS3Display", "Face asset unavailable: %s", filename);
                break;
            }
            DisplayLockGuard lock(this);
            if (gif_controller_) {
                gif_controller_->Stop();
                gif_controller_.reset();
            }
            auto& image = face_images_[i];
            image.header.magic = LV_IMAGE_HEADER_MAGIC;
            image.header.cf = LV_COLOR_FORMAT_RGB565;
            image.header.w = kFaceWidth;
            image.header.h = kFaceHeight;
            image.header.stride = kFaceWidth * 2;
            image.data = static_cast<const uint8_t*>(data);
            image.data_size = size;
            lv_image_set_src(emoji_image_, nullptr);
            lv_image_set_src(emoji_image_, &image);
            lv_obj_center(emoji_image_);
            lv_obj_add_flag(emoji_label_, LV_OBJ_FLAG_HIDDEN);
            lv_obj_remove_flag(emoji_image_, LV_OBJ_FLAG_HIDDEN);
            return;
        }
        LcdDisplay::SetEmotion(emotion);
    }
};
