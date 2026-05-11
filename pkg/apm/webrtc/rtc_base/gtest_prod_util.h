#ifndef RTC_BASE_GTEST_PROD_UTIL_H_
#define RTC_BASE_GTEST_PROD_UTIL_H_
#define FRIEND_TEST(test_case_name, test_name) \
  friend class test_case_name##_##test_name##_Test
#define FRIEND_TEST_ALL_PREFIXES(test_case_name, test_name) \
  FRIEND_TEST(test_case_name, test_name)
#endif
