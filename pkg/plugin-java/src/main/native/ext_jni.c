/* JNI binding for the guest half of the shared C engine. The engine validates the
 * ext.plugin/v1 envelope and serves the descriptor; Java supplies the handler. */
#include <jni.h>
#include <stdlib.h>
#include <string.h>

#include "ext_guest.h"

#define CLASS "io/github/webong/ext/plugin/"

typedef struct {
  JavaVM *vm;
  jobject self;       /* global reference to the Guest */
  jmethodID dispatch; /* byte[] dispatch(byte[] request, int remainingMs) */
} binding;

static JNIEnv *attach(JavaVM *vm, int *attached) {
  JNIEnv *env = NULL;
  *attached = 0;
  jint s = (*vm)->GetEnv(vm, (void **)&env, JNI_VERSION_1_6);
  if (s == JNI_OK) return env;
  if (s != JNI_EDETACHED) return NULL;
#ifdef __ANDROID__
  if ((*vm)->AttachCurrentThread(vm, &env, NULL) != JNI_OK) return NULL;
#else
  if ((*vm)->AttachCurrentThread(vm, (void **)&env, NULL) != JNI_OK) return NULL;
#endif
  *attached = 1;
  return env;
}

static void throw_status(JNIEnv *env, ext_status status) {
  jclass cls = (*env)->FindClass(env, CLASS "GuestException");
  if (!cls) return;
  jmethodID ctor = (*env)->GetMethodID(env, cls, "<init>", "(I)V");
  if (!ctor) return;
  jobject e = (*env)->NewObject(env, cls, ctor, (jint)status);
  if (e) (*env)->Throw(env, (jthrowable)e);
}

/* Dispatch returns one kind byte (0 payload, 1 public error) then JSON. Any
 * exception or null result is a private failure, which the engine sanitizes. */
static ext_status handle(void *user, void *call_user, const uint8_t *request,
                         size_t len, uint32_t remaining_ms, ext_guest_emit emit,
                         void *sink) {
  (void)call_user;
  binding *b = user;
  int attached;
  JNIEnv *env = attach(b->vm, &attached);
  if (!env) return EXT_IO;
  ext_status status = EXT_IO;
  jbyteArray in = (*env)->NewByteArray(env, (jsize)len);
  if (in) {
    (*env)->SetByteArrayRegion(env, in, 0, (jsize)len, (const jbyte *)request);
    jbyteArray out = (jbyteArray)(*env)->CallObjectMethod(env, b->self, b->dispatch,
                                                          in, (jint)remaining_ms);
    if ((*env)->ExceptionCheck(env)) {
      (*env)->ExceptionClear(env);
    } else if (out) {
      jsize n = (*env)->GetArrayLength(env, out);
      if (n >= 1) {
        uint8_t *copy = malloc((size_t)n);
        if (copy) {
          (*env)->GetByteArrayRegion(env, out, 0, n, (jbyte *)copy);
          status = emit(sink, copy[0], copy + 1, (size_t)n - 1);
          free(copy);
        } else {
          status = EXT_NOMEM;
        }
      }
      (*env)->DeleteLocalRef(env, out);
    }
    (*env)->DeleteLocalRef(env, in);
  }
  if (attached) (*b->vm)->DetachCurrentThread(b->vm);
  return status;
}

/* One Java Guest: the engine guest plus the binding its handler callback uses. */
typedef struct {
  ext_guest *guest;
  binding b;
} session;

JNIEXPORT jlong JNICALL Java_io_github_webong_ext_plugin_NativeEngine_create(
    JNIEnv *env, jclass cls, jbyteArray descriptor, jint max_call_ms, jobject guest) {
  (void)cls;
  jsize n = (*env)->GetArrayLength(env, descriptor);
  uint8_t *text = malloc(n ? (size_t)n : 1);
  session *s = calloc(1, sizeof *s);
  if (!text || !s) {
    free(text);
    free(s);
    throw_status(env, EXT_NOMEM);
    return 0;
  }
  (*env)->GetByteArrayRegion(env, descriptor, 0, n, (jbyte *)text);
  jclass guest_class = (*env)->GetObjectClass(env, guest);
  s->b.dispatch = (*env)->GetMethodID(env, guest_class, "dispatch", "([BI)[B");
  s->b.self = (*env)->NewGlobalRef(env, guest);
  if (!s->b.dispatch || !s->b.self || (*env)->GetJavaVM(env, &s->b.vm) != JNI_OK) {
    if (s->b.self) (*env)->DeleteGlobalRef(env, s->b.self);
    free(text);
    free(s);
    return 0; /* a pending Java exception, if any, propagates */
  }
  ext_guest_options options = {EXT_HOST_ABI_VERSION, sizeof options, text, (size_t)n,
                               (uint32_t)max_call_ms, &s->b, handle};
  ext_status status = ext_guest_create(&options, &s->guest);
  free(text); /* the engine snapshots the descriptor */
  if (status != EXT_OK) {
    (*env)->DeleteGlobalRef(env, s->b.self);
    free(s);
    throw_status(env, status);
    return 0;
  }
  return (jlong)(intptr_t)s;
}

static ext_guest *guest_of(jlong handle) { return ((session *)(intptr_t)handle)->guest; }

JNIEXPORT jbyteArray JNICALL Java_io_github_webong_ext_plugin_NativeEngine_descriptor(
    JNIEnv *env, jclass cls, jlong handle) {
  (void)cls;
  ext_buffer out = {0};
  ext_status status = ext_guest_descriptor(guest_of(handle), &out);
  if (status != EXT_OK) {
    throw_status(env, status);
    return NULL;
  }
  jbyteArray result = (*env)->NewByteArray(env, (jsize)out.len);
  if (result) (*env)->SetByteArrayRegion(env, result, 0, (jsize)out.len, (const jbyte *)out.data);
  ext_buffer_free(&out);
  return result;
}

JNIEXPORT jbyteArray JNICALL Java_io_github_webong_ext_plugin_NativeEngine_invoke(
    JNIEnv *env, jclass cls, jlong handle, jbyteArray request, jint timeout_ms) {
  (void)cls;
  jsize n = (*env)->GetArrayLength(env, request);
  uint8_t *in = malloc(n ? (size_t)n : 1);
  if (!in) {
    throw_status(env, EXT_NOMEM);
    return NULL;
  }
  (*env)->GetByteArrayRegion(env, request, 0, n, (jbyte *)in);
  ext_buffer out = {0};
  ext_status status = ext_guest_invoke(guest_of(handle), in, (size_t)n, (uint32_t)timeout_ms, NULL, &out);
  free(in);
  if (status != EXT_OK) {
    ext_buffer_free(&out);
    throw_status(env, status);
    return NULL;
  }
  jbyteArray result = (*env)->NewByteArray(env, (jsize)out.len);
  if (result) (*env)->SetByteArrayRegion(env, result, 0, (jsize)out.len, (const jbyte *)out.data);
  ext_buffer_free(&out);
  return result;
}

JNIEXPORT jint JNICALL Java_io_github_webong_ext_plugin_NativeEngine_validateJson(
    JNIEnv *env, jclass cls, jbyteArray data) {
  (void)cls;
  jsize n = (*env)->GetArrayLength(env, data);
  uint8_t *in = malloc(n ? (size_t)n : 1);
  if (!in) return EXT_NOMEM;
  (*env)->GetByteArrayRegion(env, data, 0, n, (jbyte *)in);
  ext_status status = ext_host_validate_json(in, (size_t)n);
  free(in);
  return status;
}

JNIEXPORT void JNICALL Java_io_github_webong_ext_plugin_NativeEngine_destroy(
    JNIEnv *env, jclass cls, jlong handle) {
  (void)cls;
  session *s = (session *)(intptr_t)handle;
  if (!s) return;
  ext_guest_destroy(s->guest);
  (*env)->DeleteGlobalRef(env, s->b.self);
  free(s);
}
