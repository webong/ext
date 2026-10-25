#ifndef EXT_ENDPOINT_H
#define EXT_ENDPOINT_H
#include "wire.h"

/* The loopback data endpoint a plugin's result declares and a host admits or
 * refuses. The reference is pkg/ext-go/engine/endpoint: Decode, Check and
 * Validate. The grant (a session-bound lifetime, one-time tokens, dialing) is the
 * binding's; the engine decides only whether an endpoint is acceptable, and says
 * so with a status, never with text: an endpoint is a credential, and nothing
 * here copies a part of one into an error. */

/* Endpoint bounds, in bytes. */
#define EXT_ENDPOINT_MAX_ADDRESS 255u
#define EXT_ENDPOINT_MAX_PATH 1024u
#define EXT_ENDPOINT_MAX_TOKEN 512u
/* The lifetime a policy allows when it names none: one hour. */
#define EXT_ENDPOINT_DEFAULT_LIFETIME_MS (60 * 60 * 1000LL)

/* What a host will admit. */
typedef struct {
  yyjson_val *schemes;     /* an array of strings, or NULL: nothing is admitted */
  int64_t lifetime_ms;     /* how far ahead an expiry may be; not positive uses the default */
  int has_now;             /* now is given, otherwise the wall clock */
  int64_t now_seconds;
  int32_t now_nanoseconds;
} ext_endpoint_policy;

/* Reads a policy object: {"schemes":[..],"maxLifetimeMilliseconds":n,"now":".."},
 * every field optional, unknown fields refused. Zero when it is not one. */
int ext_endpoint_policy_parse(yyjson_val *object, ext_endpoint_policy *policy);

/* Decode and Check. EXT_OK for an endpoint of acceptable shape; EXT_INVALID for
 * one that is malformed (an unknown or duplicate field, a wrong type, an
 * unsupported scheme, an address that is not a host and a port, a path or token
 * out of bounds, no expiry); EXT_DENIED for an address that is not on a loopback
 * host. */
ext_status ext_endpoint_check(yyjson_val *endpoint);
/* Check, then the policy: EXT_DENIED when no scheme is admitted or this one is
 * not, EXT_INVALID for a policy that names a scheme there is none of or an
 * expiry beyond the allowed lifetime, EXT_EXPIRED for one already past. */
ext_status ext_endpoint_validate(yyjson_val *endpoint,
                                 const ext_endpoint_policy *policy);
#endif
