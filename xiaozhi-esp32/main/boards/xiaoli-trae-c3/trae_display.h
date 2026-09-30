#pragma once

#include "display/lcd_display.h"
#include <algorithm>

// This overlay belongs only to the 240x320 Trae board.
class TraeDisplay : public SpiLcdDisplay {
    lv_obj_t* volume_overlay_ = nullptr;
    lv_obj_t* volume_bar_ = nullptr;
    lv_obj_t* volume_label_ = nullptr;
    lv_timer_t* volume_timer_ = nullptr;

public:
    using SpiLcdDisplay::SpiLcdDisplay;

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
