#include "bridge.h"

#include "api/audio/builtin_audio_processing_builder.h"
#include "api/environment/environment_factory.h"
#include "api/scoped_refptr.h"
#include "modules/audio_processing/include/audio_processing.h"

#include <memory>

struct ApmInstance {
    webrtc::scoped_refptr<webrtc::AudioProcessing> apm;
};

extern "C" {

ApmHandle apm_create(int echo, int gain, int hpf, int ns,
                     int capture_ch, int render_ch, int* err) {
    (void)capture_ch;
    (void)render_ch;

    auto apm = webrtc::BuiltinAudioProcessingBuilder().Build(
        webrtc::CreateEnvironment());
    if (!apm) {
        if (err) *err = -1;
        return nullptr;
    }

    webrtc::AudioProcessing::Config config;
    config.echo_canceller.enabled = (echo != 0);
    config.gain_controller1.enabled = false;
    config.gain_controller2.enabled = (gain != 0);
    config.high_pass_filter.enabled = (hpf != 0);
    config.noise_suppression.enabled = (ns != 0);
    if (ns) {
        config.noise_suppression.level =
            webrtc::AudioProcessing::Config::NoiseSuppression::kHigh;
    }

    apm->ApplyConfig(config);
    apm->Initialize();

    auto* inst = new ApmInstance{std::move(apm)};
    if (err) *err = 0;
    return static_cast<ApmHandle>(inst);
}

void apm_destroy(ApmHandle h) {
    if (h) {
        delete static_cast<ApmInstance*>(h);
    }
}

int apm_process_capture(ApmHandle h, int16_t* samples, int num_channels) {
    auto* inst = static_cast<ApmInstance*>(h);
    // 10ms at 48kHz = 480 samples per channel
    webrtc::StreamConfig stream_cfg(48000, num_channels);
    return inst->apm->ProcessStream(samples, stream_cfg, stream_cfg, samples);
}

int apm_process_render(ApmHandle h, int16_t* samples, int num_channels) {
    auto* inst = static_cast<ApmInstance*>(h);
    webrtc::StreamConfig stream_cfg(48000, num_channels);
    return inst->apm->ProcessReverseStream(samples, stream_cfg, stream_cfg, samples);
}

void apm_set_stream_delay_ms(ApmHandle h, int delay_ms) {
    auto* inst = static_cast<ApmInstance*>(h);
    inst->apm->set_stream_delay_ms(delay_ms);
}

int apm_stream_delay_ms(ApmHandle h) {
    auto* inst = static_cast<ApmInstance*>(h);
    return inst->apm->stream_delay_ms();
}

void apm_get_stats(ApmHandle h, ApmStats* out) {
    if (!h || !out) return;
    auto* inst = static_cast<ApmInstance*>(h);
    auto stats = inst->apm->GetStatistics();

    out->has_erl = stats.echo_return_loss.has_value() ? 1 : 0;
    out->echo_return_loss = stats.echo_return_loss.value_or(0.0);

    out->has_erle = stats.echo_return_loss_enhancement.has_value() ? 1 : 0;
    out->echo_return_loss_enhancement = stats.echo_return_loss_enhancement.value_or(0.0);

    out->has_divergent = stats.divergent_filter_fraction.has_value() ? 1 : 0;
    out->divergent_filter_fraction = stats.divergent_filter_fraction.value_or(0.0);

    out->has_delay = stats.delay_ms.has_value() ? 1 : 0;
    out->delay_ms = stats.delay_ms.value_or(0);

    out->has_residual_echo = stats.residual_echo_likelihood.has_value() ? 1 : 0;
    out->residual_echo_likelihood = stats.residual_echo_likelihood.value_or(0.0);
}

} // extern "C"
