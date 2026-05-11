#ifndef API_RTP_HEADERS_H_
#define API_RTP_HEADERS_H_

#include <stdint.h>
#include <cstring>

namespace webrtc {

struct RTPHeader {
  bool markerBit = false;
  uint8_t payloadType = 0;
  uint16_t sequenceNumber = 0;
  uint32_t timestamp = 0;
  uint32_t ssrc = 0;
  uint8_t numCSRCs = 0;
  uint32_t arrOfCSRCs[15] = {};
  size_t paddingLength = 0;
  size_t headerLength = 0;
};

}  // namespace webrtc
#endif  // API_RTP_HEADERS_H_
