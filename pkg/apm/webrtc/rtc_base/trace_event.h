#ifndef RTC_BASE_TRACE_EVENT_H_
#define RTC_BASE_TRACE_EVENT_H_

// Stub - perfetto tracing not needed for APM standalone build
#define TRACE_EVENT0(category, name)
#define TRACE_EVENT1(category, name, arg1_name, arg1_val)
#define TRACE_EVENT2(category, name, arg1_name, arg1_val, arg2_name, arg2_val)
#define TRACE_EVENT_INSTANT0(category, name)
#define TRACE_EVENT_INSTANT1(category, name, arg1_name, arg1_val)
#define TRACE_EVENT_BEGIN0(category, name)
#define TRACE_EVENT_END0(category, name)
#define TRACE_EVENT_ASYNC_BEGIN0(category, name, id)
#define TRACE_EVENT_ASYNC_END0(category, name, id)

#endif  // RTC_BASE_TRACE_EVENT_H_
