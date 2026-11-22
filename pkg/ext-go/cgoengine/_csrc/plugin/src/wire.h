#ifndef EXT_WIRE_H
#define EXT_WIRE_H
#include "ext_host.h"
#include "yyjson.h"
yyjson_doc *ext_parse(const uint8_t *, size_t);
int ext_descriptor(yyjson_val *);
int ext_match(yyjson_val *, yyjson_val *);
/* Whether a valid descriptor lists any reverse operation (host operations the
 * plugin may call) and whether any operation it serves declares progress. Both
 * need frames beyond a single response, which only some hosts carry. */
int ext_descriptor_declares_reverse(yyjson_val *);
int ext_descriptor_declares_progress(yyjson_val *);
/* The event rate a valid request carries, 0 meaning the default; declared says
 * whether it carries one at all. */
void ext_request_progress(yyjson_val *request, int *declared, int64_t *events,
                          int64_t *bytes);
ext_status ext_request(yyjson_val *, yyjson_val *, int64_t *deadline_ms);
ext_status ext_response(yyjson_val *, const char *id);
/* A retry hint beyond one year is invalid (plugin.MaxRetryAfterMilliseconds). */
#define EXT_MAX_RETRY_AFTER_MS ((int64_t)365 * 24 * 60 * 60 * 1000)
/* Whether v is a RemoteError a peer would accept. */
int ext_error_valid(yyjson_val *);
/* A JSON integer that fits int64 (the parser keeps numbers as raw text). */
int ext_int64(yyjson_val *, int64_t *out);
/* The wall-clock time in milliseconds of an RFC 3339 string as a request
 * deadline, or -1 when it is not one, or is the zero time. */
int64_t ext_deadline_ms(yyjson_val *);
/* An RFC 3339 time as the seconds since the Unix epoch and the nanoseconds of the
 * instant, exactly; zero when v is not one. ext_instant_is_zero tells Go's zero
 * time, which a field left out decodes to. */
int ext_instant(yyjson_val *, int64_t *seconds, int32_t *nanoseconds);
int ext_instant_is_zero(int64_t seconds, int32_t nanoseconds);
/* ext_lookup over any list of contracts, such as a descriptor's "reverse". */
yyjson_val *ext_lookup_in(yyjson_val *contracts, yyjson_val *ref,
                          yyjson_val *name);
int64_t ext_wall_ms(void);
/* The wall clock as seconds since the Unix epoch and nanoseconds. */
void ext_wall_clock(int64_t *seconds, int32_t *nanoseconds);
int ext_fields(yyjson_val *, const char *const *, size_t);
int ext_text(yyjson_val *, int revision, int optional);
int ext_same(yyjson_val *, yyjson_val *);
int ext_identity(yyjson_val *);
int ext_same_identity(yyjson_val *, yyjson_val *);
int ext_ref(yyjson_val *);
int ext_same_ref(yyjson_val *, yyjson_val *);
yyjson_val *ext_lookup(yyjson_val *, yyjson_val *, yyjson_val *);
/* yyjson_arr_get(arr, i) costs O(i) when the elements are objects or arrays: it
 * steps over the siblings. A loop that indexes inside another loop is then
 * cubic on a manifest that fills its arrays. Visit the elements before
 * position upto with this instead, which is O(1) a step; it is used like
 * yyjson_arr_foreach, and the statement that follows runs for each element. */
#define EXT_ARR_BEFORE(arr, upto, idx, max, val)                               \
  yyjson_arr_foreach(arr, idx, max, val) if ((idx) >= (upto)) break; else
/* The elements of arr as pointers in one allocation, to index in constant time
 * instead of calling yyjson_arr_get. NULL means memory ran out. The caller
 * frees it. */
yyjson_val **ext_arr_snapshot(yyjson_val *arr);
#define EXT_FIELDS(v, ...)                                                     \
  ext_fields(v, (const char *const[]){__VA_ARGS__},                            \
             sizeof((const char *const[]){__VA_ARGS__}) / sizeof(char *))
#endif
