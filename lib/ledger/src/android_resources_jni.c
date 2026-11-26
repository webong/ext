#ifdef __ANDROID__
#include <jni.h>
#include "ext_ledger.h"

/* JNI carries only the ledger's wire record. Resource collection stays in C. */
JNIEXPORT jstring JNICALL
Java_io_github_webong_ext_system_android_SystemResourcesObserver_collectNative(
    JNIEnv *env, jclass ignored) {
  (void)ignored;
  char *json = NULL;
  size_t length = 0;
  int code = ext_ledger_collect(NULL, "resources", &json, &length);
  if (code != EXT_LEDGER_OK || !json) {
    ext_ledger_free(json);
    jclass error = (*env)->FindClass(env, "java/lang/IllegalStateException");
    if (error) (*env)->ThrowNew(env, error, ext_ledger_code_name(code));
    return NULL;
  }
  jstring result = (*env)->NewStringUTF(env, json);
  ext_ledger_free(json);
  return result;
}
#endif
