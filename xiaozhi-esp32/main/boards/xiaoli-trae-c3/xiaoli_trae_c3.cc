#include "wifi_board.h"
#include "codecs/es8311_audio_codec.h"
#include "trae_display.h"
#include <esp_adc/adc_cali_scheme.h>
#include "application.h"
#include "button.h"
#include "config.h"

#include <algorithm>
#include <memory>
#include <driver/i2c_master.h>
#include <driver/spi_common.h>
#include <esp_lcd_panel_vendor.h>
#include <esp_log.h>

static_assert(CONFIG_XIAOLI_C3_CHAT_BUTTON != CONFIG_XIAOLI_C3_VOLUME_DOWN_BUTTON &&
              CONFIG_XIAOLI_C3_CHAT_BUTTON != CONFIG_XIAOLI_C3_VOLUME_UP_BUTTON &&
              CONFIG_XIAOLI_C3_VOLUME_DOWN_BUTTON != CONFIG_XIAOLI_C3_VOLUME_UP_BUTTON,
              "Each C3 button must have a distinct role");

class TraeAudioCodec : public Es8311AudioCodec {
public:
    using Es8311AudioCodec::Es8311AudioCodec;
    void SetOutputVolume(int volume) override {
        Es8311AudioCodec::SetOutputVolume(volume);
        Application::GetInstance().Schedule([volume]() {
            static_cast<TraeDisplay*>(Board::GetInstance().GetDisplay())->ShowVolume(volume);
        });
    }
};

class XiaoliTraeC3 : public WifiBoard {
    i2c_master_bus_handle_t i2c_bus_ = nullptr;
    TraeDisplay* display_ = nullptr;
    adc_oneshot_unit_handle_t button_adc_ = nullptr;
    adc_cali_handle_t button_calibration_ = nullptr;
    std::unique_ptr<AdcButton> buttons_[3];

    void InitializeAudioBus() {
        i2c_master_bus_config_t config = {};
        config.i2c_port = I2C_NUM_0;
        config.sda_io_num = AUDIO_CODEC_I2C_SDA_PIN;
        config.scl_io_num = AUDIO_CODEC_I2C_SCL_PIN;
        config.clk_source = I2C_CLK_SRC_DEFAULT;
        config.glitch_ignore_cnt = 7;
        config.flags.enable_internal_pullup = true;
        ESP_ERROR_CHECK(i2c_new_master_bus(&config, &i2c_bus_));
        // Codec API uses the 8-bit address 0x30; probe uses the 7-bit address.
        ESP_ERROR_CHECK(i2c_master_probe(i2c_bus_, ES8311_CODEC_DEFAULT_ADDR >> 1, 1000));
    }

    void InitializeDisplay() {
        spi_bus_config_t bus = {};
        bus.mosi_io_num = DISPLAY_SPI_MOSI_PIN;
        bus.miso_io_num = GPIO_NUM_NC;
        bus.sclk_io_num = DISPLAY_SPI_SCK_PIN;
        bus.quadwp_io_num = GPIO_NUM_NC;
        bus.quadhd_io_num = GPIO_NUM_NC;
        bus.max_transfer_sz = DISPLAY_WIDTH * 80 * sizeof(uint16_t);
        ESP_ERROR_CHECK(spi_bus_initialize(SPI2_HOST, &bus, SPI_DMA_CH_AUTO));
        esp_lcd_panel_io_spi_config_t io_config = {};
        io_config.cs_gpio_num = DISPLAY_SPI_CS_PIN;
        io_config.dc_gpio_num = DISPLAY_DC_PIN;
        io_config.spi_mode = 0;
        io_config.pclk_hz = 80000000;
        io_config.trans_queue_depth = 10;
        io_config.lcd_cmd_bits = 8;
        io_config.lcd_param_bits = 8;
        esp_lcd_panel_io_handle_t io = nullptr;
        ESP_ERROR_CHECK(esp_lcd_new_panel_io_spi(SPI2_HOST, &io_config, &io));
        esp_lcd_panel_dev_config_t panel_config = {};
        panel_config.reset_gpio_num = GPIO_NUM_NC;
        panel_config.rgb_ele_order = LCD_RGB_ELEMENT_ORDER_RGB;
        panel_config.bits_per_pixel = 16;
        esp_lcd_panel_handle_t panel = nullptr;
        ESP_ERROR_CHECK(esp_lcd_new_panel_st7789(io, &panel_config, &panel));
        ESP_ERROR_CHECK(esp_lcd_panel_reset(panel));
        ESP_ERROR_CHECK(esp_lcd_panel_init(panel));
        // Preserve the panel tuning sequence from the factory firmware.
        struct Command { int id; uint8_t data[14]; size_t size; };
        static const Command commands[] = {
            {0xb2, {5, 5, 0, 0x33, 0x33}, 5}, {0xb7, {0x35}, 1},
            {0xbb, {0x21}, 1}, {0xc0, {0x2c}, 1}, {0xc2, {1}, 1},
            {0xc3, {0x0b}, 1}, {0xc4, {0x20}, 1}, {0xc6, {0x0f}, 1},
            {0xd0, {0xa7, 0xa1}, 2}, {0xd0, {0xa4, 0xa1}, 2},
            {0xd6, {0xa1}, 1},
            {0xe0, {0xd0,4,8,0x0a,9,5,0x2d,0x43,0x49,9,0x16,0x15,0x26,0x2b}, 14},
            {0xe1, {0xd0,3,9,0x0a,0x0a,6,0x2e,0x44,0x40,0x3a,0x15,0x15,0x26,0x2a}, 14},
        };
        for (const auto& command : commands) {
            ESP_ERROR_CHECK(esp_lcd_panel_io_tx_param(io, command.id, command.data, command.size));
        }
        vTaskDelay(pdMS_TO_TICKS(10));
        ESP_ERROR_CHECK(esp_lcd_panel_invert_color(panel, true));
        ESP_ERROR_CHECK(esp_lcd_panel_mirror(panel, false, false));
        ESP_ERROR_CHECK(esp_lcd_panel_disp_on_off(panel, true));
        display_ = new TraeDisplay(io, panel, DISPLAY_WIDTH, DISPLAY_HEIGHT,
                                     0, 0, false, false, false);
        GetBacklight()->RestoreBrightness();
    }

    void InitializeButtons() {
        adc_oneshot_unit_init_cfg_t unit = {};
        unit.unit_id = ADC_UNIT_1;
        ESP_ERROR_CHECK(adc_oneshot_new_unit(&unit, &button_adc_));
        adc_cali_curve_fitting_config_t calibration = {};
        calibration.unit_id = ADC_UNIT_1;
        calibration.chan = ADC_CHANNEL_0;
        calibration.atten = ADC_ATTEN_DB_12;
        calibration.bitwidth = ADC_BITWIDTH_DEFAULT;
        ESP_ERROR_CHECK(adc_cali_create_scheme_curve_fitting(&calibration, &button_calibration_));
        // Factory ADC1 channel 0 voltage windows; do not use GPIO9 as BOOT:
        // GPIO9 is connected to the display's MOSI signal on this board.
        constexpr uint16_t minimum[] = {0, 150, 447};
        constexpr uint16_t maximum[] = {150, 447, 1900};
        for (uint8_t i = 0; i < 3; ++i) {
            button_adc_config_t config = {};
            config.adc_handle = &button_adc_;
            config.unit_id = ADC_UNIT_1;
            config.adc_channel = ADC_CHANNEL_0;
            config.button_index = i;
            config.min = minimum[i];
            config.max = maximum[i];
            buttons_[i] = std::make_unique<AdcButton>(config);
            buttons_[i]->OnPressDown([this, i]() {
                int raw = 0, voltage = 0;
                if (adc_oneshot_read(button_adc_, ADC_CHANNEL_0, &raw) == ESP_OK &&
                    adc_cali_raw_to_voltage(button_calibration_, raw, &voltage) == ESP_OK) {
                    ESP_LOGI("XiaoliTraeC3", "Button voltage: %d mV (index %u)", voltage, static_cast<unsigned>(i));
                }
                ESP_LOGI("XiaoliTraeC3", "ADC button %u pressed", static_cast<unsigned>(i));
            });
        }
        buttons_[CONFIG_XIAOLI_C3_CHAT_BUTTON]->OnClick([this]() {
            Application::GetInstance().Schedule([this]() {
                if (Application::GetInstance().GetDeviceState() == kDeviceStateStarting) {
                    EnterWifiConfigMode();
                } else {
                    Application::GetInstance().ToggleChatState();
                }
            });
        });
        buttons_[CONFIG_XIAOLI_C3_CHAT_BUTTON]->OnLongPress([this]() {
            Application::GetInstance().Schedule([this]() { EnterWifiConfigMode(); });
        });
        buttons_[CONFIG_XIAOLI_C3_VOLUME_DOWN_BUTTON]->OnClick([this]() {
            Application::GetInstance().Schedule([this]() {
                auto codec = GetAudioCodec();
                codec->SetOutputVolume(std::max(0, codec->output_volume() - 10));
            });
        });
        buttons_[CONFIG_XIAOLI_C3_VOLUME_UP_BUTTON]->OnClick([this]() {
            Application::GetInstance().Schedule([this]() {
                auto codec = GetAudioCodec();
                codec->SetOutputVolume(std::min(100, codec->output_volume() + 10));
            });
        });
    }

public:
    XiaoliTraeC3() {
        ESP_LOGI("XiaoliTraeC3", "ES8311 SDA=10 SCL=7 MCLK=6 BCLK=5 WS=3 DOUT=2 DIN=4");
        InitializeAudioBus();
        InitializeDisplay();
        InitializeButtons();
    }

    AudioCodec* GetAudioCodec() override {
        static TraeAudioCodec codec(i2c_bus_, I2C_NUM_0,
            AUDIO_INPUT_SAMPLE_RATE, AUDIO_OUTPUT_SAMPLE_RATE,
            AUDIO_I2S_GPIO_MCLK, AUDIO_I2S_GPIO_BCLK, AUDIO_I2S_GPIO_WS,
            AUDIO_I2S_GPIO_DOUT, AUDIO_I2S_GPIO_DIN,
            AUDIO_CODEC_PA_PIN, ES8311_CODEC_DEFAULT_ADDR);
        return &codec;
    }
    Display* GetDisplay() override { return display_; }
    Backlight* GetBacklight() override {
        static PwmBacklight backlight(DISPLAY_BACKLIGHT_PIN, false);
        return &backlight;
    }
};

DECLARE_BOARD(XiaoliTraeC3);
