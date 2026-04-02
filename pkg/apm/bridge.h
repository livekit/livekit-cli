#ifndef LK_APM_BRIDGE_H
#define LK_APM_BRIDGE_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef void* ApmHandle;

// Create an APM instance. Returns NULL on error, sets *err to non-zero.
ApmHandle apm_create(int echo, int gain, int hpf, int ns,
                     int capture_ch, int render_ch, int* err);

// Destroy an APM instance.
void apm_destroy(ApmHandle h);

// Process a 10ms capture frame in-place. Returns 0 on success.
int apm_process_capture(ApmHandle h, int16_t* samples, int num_channels);

// Process a 10ms render (far-end/playback) frame in-place. Returns 0 on success.
int apm_process_render(ApmHandle h, int16_t* samples, int num_channels);

// Set the stream delay in milliseconds for echo cancellation.
void apm_set_stream_delay_ms(ApmHandle h, int delay_ms);

// Get the current stream delay in milliseconds.
int apm_stream_delay_ms(ApmHandle h);

// AEC statistics returned by apm_get_stats.
typedef struct {
    int    has_erl;
    double echo_return_loss;          // ERL in dB
    int    has_erle;
    double echo_return_loss_enhancement; // ERLE in dB
    int    has_divergent;
    double divergent_filter_fraction;
    int    has_delay;
    int    delay_ms;
    int    has_residual_echo;
    double residual_echo_likelihood;
} ApmStats;

// Get current AEC statistics.
void apm_get_stats(ApmHandle h, ApmStats* out);

#ifdef __cplusplus
}
#endif

#endif // LK_APM_BRIDGE_H
