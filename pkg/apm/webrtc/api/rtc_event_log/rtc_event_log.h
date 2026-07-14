#ifndef API_RTC_EVENT_LOG_RTC_EVENT_LOG_H_
#define API_RTC_EVENT_LOG_RTC_EVENT_LOG_H_

#include <memory>

namespace webrtc {

class RtcEventLog {
 public:
  virtual ~RtcEventLog() = default;
};

class RtcEventLogNull : public RtcEventLog {
 public:
  ~RtcEventLogNull() override = default;
};

}  // namespace webrtc
#endif  // API_RTC_EVENT_LOG_RTC_EVENT_LOG_H_
