#ifndef API_RTP_PACKET_INFO_H_
#define API_RTP_PACKET_INFO_H_

#include <cstdint>
#include "api/rtp_headers.h"

namespace webrtc {

struct AbsoluteCaptureTime {
  uint64_t absolute_capture_timestamp = 0;
  int64_t estimated_capture_clock_offset = 0;
};

struct RtpPacketInfo {
  uint32_t ssrc() const { return 0; }
};

}  // namespace webrtc
#endif  // API_RTP_PACKET_INFO_H_
