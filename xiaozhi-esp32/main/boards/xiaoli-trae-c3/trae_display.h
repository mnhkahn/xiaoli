#pragma once

#include "display/lcd_display.h"
#include "display/lvgl_display/lvgl_theme.h"
#include "assets.h"
#include "board.h"
#include <algorithm>
#include <array>
#include <cstring>

// This overlay belongs only to the 240x320 Trae board.
class TraeDisplay : public SpiLcdDisplay {
    static constexpr int kFaceWidth = 168;
    static constexpr int kFaceHeight = 200;
    static constexpr std::array<const char*, 24> kFaceNames = {
        "neutral", "happy", "laughing", "funny", "sad", "angry", "crying",
        "loving", "embarrassed", "surprised", "shocked", "thinking", "winking",
        "cool", "relaxed", "delicious", "kissy", "confident", "sleepy",
        "silly", "confused", "listening", "speaking", "blink"
    };
    std::array<lv_image_dsc_t, kFaceNames.size()> face_images_{};
    lv_obj_t* battery_percent_label_ = nullptr;
    lv_obj_t* volume_overlay_ = nullptr;
    lv_obj_t* volume_bar_ = nullptr;
    lv_obj_t* volume_label_ = nullptr;
    lv_timer_t* volume_timer_ = nullptr;

public:
    using SpiLcdDisplay::SpiLcdDisplay;

    void SetupUI() override {
        auto* theme = LvglThemeManager::GetInstance().GetTheme("dark");
        theme->set_background_color(lv_color_hex(0x041220));
        theme->set_chat_background_color(lv_color_hex(0x041220));
        current_theme_ = theme;
        LcdDisplay::SetupUI();
        DisplayLockGuard lock(this);
        lv_obj_set_size(emoji_box_, 220, 220);
        lv_obj_align(emoji_box_, LV_ALIGN_CENTER, 0, 0);
        lv_obj_center(emoji_label_);
        lv_obj_center(emoji_image_);
        battery_percent_label_ = lv_label_create(lv_obj_get_parent(battery_label_));
        lv_obj_set_style_text_color(battery_percent_label_, lv_color_white(), 0);
        lv_obj_set_style_text_font(battery_percent_label_, theme->text_font()->font(), 0);
        lv_obj_set_style_margin_left(battery_percent_label_, 2, 0);
        lv_label_set_text(battery_percent_label_, "");
    }

    void UpdateStatusBar(bool update_all = false) override {
        LvglDisplay::UpdateStatusBar(update_all);
        if (!battery_percent_label_) return;
        int level = 0;
        bool charging = false, discharging = false;
        bool valid = Board::GetInstance().GetBatteryLevel(level, charging, discharging);
        DisplayLockGuard lock(this);
        if (valid) {
            lv_label_set_text_fmt(battery_percent_label_, "%d%%", level);
        } else {
            lv_label_set_text(battery_percent_label_, "");
            lv_label_set_text(battery_label_, "");
            battery_icon_ = nullptr;
        }
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
                ESP_LOGW("TraeDisplay", "Face asset unavailable: %s", filename);
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

    ~TraeDisplay() override {
        DisplayLockGuard lock(this);
        if (volume_timer_) lv_timer_delete(volume_timer_);
        if (volume_overlay_) lv_obj_delete(volume_overlay_);
    }

    void ShowVolume(int volume) {
        if (!IsSetupUICalled()) return;
        DisplayLockGuard lock(this);
        if (!volume_overlay_) {
            volume_overlay_ = lv_obj_create(lv_display_get_layer_top(display_));
            lv_obj_set_size(volume_overlay_, 48, height_ - 40);
            lv_obj_align(volume_overlay_, LV_ALIGN_RIGHT_MID, -4, 0);
            lv_obj_remove_flag(volume_overlay_, LV_OBJ_FLAG_SCROLLABLE);
            lv_obj_set_style_bg_color(volume_overlay_, lv_color_hex(0x17202b), 0);
            lv_obj_set_style_bg_opa(volume_overlay_, LV_OPA_COVER, 0);
            lv_obj_set_style_border_width(volume_overlay_, 0, 0);
            lv_obj_set_style_radius(volume_overlay_, 18, 0);
            lv_obj_set_style_pad_all(volume_overlay_, 4, 0);

            volume_label_ = lv_label_create(volume_overlay_);
            lv_obj_set_style_text_color(volume_label_, lv_color_white(), 0);
            lv_obj_align(volume_label_, LV_ALIGN_TOP_MID, 0, 4);
            volume_bar_ = lv_bar_create(volume_overlay_);
            lv_obj_set_size(volume_bar_, 16, height_ - 100);
            lv_obj_align(volume_bar_, LV_ALIGN_BOTTOM_MID, 0, -6);
            lv_bar_set_range(volume_bar_, 0, 100);
            lv_obj_set_style_bg_color(volume_bar_, lv_color_hex(0x394553), LV_PART_MAIN);
            lv_obj_set_style_bg_color(volume_bar_, lv_color_hex(0x58d6ae), LV_PART_INDICATOR);
            lv_obj_set_style_anim_duration(volume_bar_, 150, 0);
            volume_timer_ = lv_timer_create([](lv_timer_t* timer) {
                auto self = static_cast<TraeDisplay*>(lv_timer_get_user_data(timer));
                lv_obj_add_flag(self->volume_overlay_, LV_OBJ_FLAG_HIDDEN);
                lv_timer_pause(timer);
            }, 2500, this);
        }
        volume = std::clamp(volume, 0, 100);
        lv_label_set_text_fmt(volume_label_, "%d%%", volume);
        lv_bar_set_value(volume_bar_, volume, LV_ANIM_ON);
        lv_obj_remove_flag(volume_overlay_, LV_OBJ_FLAG_HIDDEN);
        lv_obj_move_foreground(volume_overlay_);
        lv_timer_reset(volume_timer_);
        lv_timer_resume(volume_timer_);
        ESP_LOGI("TraeDisplay", "Volume overlay: %d%%", volume);
    }
};
