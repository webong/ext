#include "ext_plugin_host.h"
#include <assert.h>
#include <stdio.h>
#include <string.h>

int main(int argc, char **argv) {
  assert(argc == 2);
  const char *path = argv[1];
  remove(path);
  uint8_t first[32] = {0}, second[32] = {0};
  second[0] = 1;
  ext_plugin_trust_state state = EXT_PLUGIN_TRUST_CHANGED;
  assert(ext_plugin_trust_state_of(path, "ext.command/v1", "example/tools", first,
                            &state) == EXT_PLUGIN_OK);
  assert(state == EXT_PLUGIN_TRUST_UNTRUSTED);
  assert(ext_plugin_trust_record(path, "ext.command/v1", "example/tools", first) == EXT_PLUGIN_OK);
  assert(ext_plugin_trust_state_of(path, "ext.command/v1", "example/tools", first,
                            &state) == EXT_PLUGIN_OK);
  assert(state == EXT_PLUGIN_TRUST_TRUSTED);
  assert(ext_plugin_trust_state_of(path, "ext.command/v1", "example/tools", second,
                            &state) == EXT_PLUGIN_OK);
  assert(state == EXT_PLUGIN_TRUST_CHANGED);
  assert(ext_plugin_trust_state_of(path, "inference.provider/v1", "example/tools",
                            first, &state) == EXT_PLUGIN_OK);
  assert(state == EXT_PLUGIN_TRUST_UNTRUSTED);
  assert(ext_plugin_trust_record(path, "inference.provider/v1", "example/tools",
                          second) == EXT_PLUGIN_OK);
  assert(ext_plugin_trust_state_of(path, "inference.provider/v1", "example/tools",
                            second, &state) == EXT_PLUGIN_OK);
  assert(state == EXT_PLUGIN_TRUST_TRUSTED);
  assert(ext_plugin_trust_revoke(path, "ext.command/v1", "example/tools") == EXT_PLUGIN_OK);
  assert(ext_plugin_trust_state_of(path, "ext.command/v1", "example/tools", first,
                            &state) == EXT_PLUGIN_OK);
  assert(state == EXT_PLUGIN_TRUST_UNTRUSTED);
  assert(ext_plugin_trust_state_of(path, "inference.provider/v1", "example/tools",
                            second, &state) == EXT_PLUGIN_OK);
  assert(state == EXT_PLUGIN_TRUST_TRUSTED);
  assert(ext_plugin_trust_record(path, "bad\nkey", "example/tools", first) == EXT_PLUGIN_INVALID);
  FILE *f = fopen(path, "wb");
  assert(f);
  assert(fwrite("{}", 1, 2, f) == 2);
  assert(!fclose(f));
  assert(ext_plugin_trust_state_of(path, "inference.provider/v1", "example/tools",
                            second, &state) == EXT_PLUGIN_INVALID);
  assert(ext_plugin_trust_record(path, "ext.command/v1", "example/tools", first) == EXT_PLUGIN_INVALID);
  remove(path);
  return 0;
}
