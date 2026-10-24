/* JNI binding for the host half of the shared C engine with the WAMR reactor backend. It hosts
 * a WebAssembly reactor in this process; the optional extwamrjni library, built only when
 * EXT_WAMR_ROOT names a WAMR checkout. The engine owns deadlines, limits and sanitizing; Java
 * supplies the verify and authorize policies and receives guest diagnostics. */
#include <jni.h>
#include <stdlib.h>
#include <string.h>

#include "ext_host.h"
#include "ext_wamr.h"

#define CLASS "io/github/webong/ext/plugin/"

typedef struct {
  JavaVM *vm;
  jobject owner;     /* global reference to the WamrPluginHost */
  jmethodID verify;  /* boolean verify(byte[]) */
  jmethodID authorize;
  jmethodID diagnostic; /* void diagnostic(byte[]) */
} binding;

typedef struct {
  ext_host *host;
  binding *b;
} session;

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
  jclass cls = (*env)->FindClass(env, CLASS "WamrException");
  if (!cls) return;
  jmethodID ctor = (*env)->GetMethodID(env, cls, "<init>", "(ILjava/lang/String;)V");
  if (!ctor) return;
  jstring reason = (*env)->NewStringUTF(env, ext_host_status_string(status));
  jobject e = (*env)->NewObject(env, cls, ctor, (jint)status, reason);
  if (e) (*env)->Throw(env, (jthrowable)e);
}

static jbyteArray to_java(JNIEnv *env, const uint8_t *data, size_t len) {
  jbyteArray out = (*env)->NewByteArray(env, (jsize)len);
  if (out) (*env)->SetByteArrayRegion(env, out, 0, (jsize)len, (const jbyte *)data);
  return out;
}

/* A policy returns zero to allow. An exception or a failure to reach Java denies. */
static int32_t policy(binding *b, jmethodID method, const uint8_t *json, size_t len) {
  int attached;
  JNIEnv *env = attach(b->vm, &attached);
  if (!env) return EXT_DENIED;
  int32_t result = EXT_DENIED;
  jbyteArray in = to_java(env, json, len);
  if (in) {
    jboolean allowed = (*env)->CallBooleanMethod(env, b->owner, method, in);
    if ((*env)->ExceptionCheck(env)) {
      (*env)->ExceptionClear(env);
    } else if (allowed) {
      result = EXT_OK;
    }
    (*env)->DeleteLocalRef(env, in);
  }
  if (attached) (*b->vm)->DetachCurrentThread(b->vm);
  return result;
}

static int32_t verify_policy(void *user, const uint8_t *json, size_t len) {
  binding *b = user;
  return policy(b, b->verify, json, len);
}

static int32_t authorize_policy(void *user, const uint8_t *json, size_t len) {
  binding *b = user;
  return policy(b, b->authorize, json, len);
}

static void diagnostic_sink(void *user, const uint8_t *data, size_t len) {
  binding *b = user;
  int attached;
  JNIEnv *env = attach(b->vm, &attached);
  if (!env) return;
  jbyteArray in = to_java(env, data, len);
  if (in) {
    (*env)->CallVoidMethod(env, b->owner, b->diagnostic, in);
    if ((*env)->ExceptionCheck(env)) (*env)->ExceptionClear(env);
    (*env)->DeleteLocalRef(env, in);
  }
  if (attached) (*b->vm)->DetachCurrentThread(b->vm);
}

typedef struct {
  uint8_t *data;
  size_t len;
} collector;

static ext_status collect(void *context, const uint8_t *data, size_t len) {
  collector *c = context;
  uint8_t *grown = realloc(c->data, c->len + len + 1);
  if (!grown) return EXT_NOMEM;
  memcpy(grown + c->len, data, len);
  c->data = grown;
  c->len += len;
  return EXT_OK;
}

static ext_status make_backend(JNIEnv *env, jbyteArray module, jint memory_pages, jint stack,
                               jlong instruction_limit, jint max_diagnostic, binding *b,
                               ext_backend_extension_context *out) {
  jsize n = (*env)->GetArrayLength(env, module);
  uint8_t *bytes = malloc(n ? (size_t)n : 1);
  if (!bytes) return EXT_NOMEM;
  (*env)->GetByteArrayRegion(env, module, 0, n, (jbyte *)bytes);
  ext_wamr_options options;
  memset(&options, 0, sizeof options);
  options.struct_size = sizeof options;
  options.memory_limit_pages = (uint32_t)memory_pages;
  options.stack_size = (uint32_t)stack;
  options.instruction_limit = (uint64_t)instruction_limit;
  options.max_diagnostic_bytes = (uint32_t)max_diagnostic;
  if (b && b->diagnostic) {
    options.diagnostic = diagnostic_sink;
    options.diagnostic_user = b;
  }
  ext_status status = ext_wamr_backend_create(bytes, (size_t)n, &options, out);
  free(bytes); /* the backend copies the module */
  return status;
}

static binding *new_binding(JNIEnv *env, jobject owner) {
  binding *b = calloc(1, sizeof *b);
  if (!b) return NULL;
  jclass cls = (*env)->GetObjectClass(env, owner);
  b->verify = (*env)->GetMethodID(env, cls, "verify", "([B)Z");
  b->authorize = (*env)->GetMethodID(env, cls, "authorize", "([B)Z");
  b->diagnostic = (*env)->GetMethodID(env, cls, "diagnostic", "([B)V");
  b->owner = (*env)->NewGlobalRef(env, owner);
  if (!b->verify || !b->authorize || !b->diagnostic || !b->owner ||
      (*env)->GetJavaVM(env, &b->vm) != JNI_OK) {
    if (b->owner) (*env)->DeleteGlobalRef(env, b->owner);
    free(b);
    return NULL;
  }
  return b;
}

static void free_binding(JNIEnv *env, binding *b) {
  if (!b) return;
  (*env)->DeleteGlobalRef(env, b->owner);
  free(b);
}

JNIEXPORT jlong JNICALL Java_io_github_webong_ext_plugin_WamrNative_create(
    JNIEnv *env, jclass cls, jbyteArray module, jbyteArray descriptor, jint memory_pages,
    jint stack, jlong instruction_limit, jint max_diagnostic, jboolean want_diagnostic,
    jobject owner) {
  (void)cls;
  binding *b = new_binding(env, owner);
  session *s = calloc(1, sizeof *s);
  if (!b || !s) {
    free_binding(env, b);
    free(s);
    return 0; /* a pending Java exception, if any, propagates */
  }
  s->b = b;
  ext_backend_extension_context backend;
  memset(&backend, 0, sizeof backend);
  ext_status status = make_backend(env, module, memory_pages, stack, instruction_limit,
                                   max_diagnostic, want_diagnostic ? b : NULL, &backend);
  if (status != EXT_OK) {
    free_binding(env, b);
    free(s);
    throw_status(env, status);
    return 0;
  }
  jsize n = (*env)->GetArrayLength(env, descriptor);
  uint8_t *text = malloc(n ? (size_t)n : 1);
  if (!text) {
    if (backend.release) backend.release(backend.user);
    free_binding(env, b);
    free(s);
    throw_status(env, EXT_NOMEM);
    return 0;
  }
  (*env)->GetByteArrayRegion(env, descriptor, 0, n, (jbyte *)text);
  ext_host_options host_options;
  memset(&host_options, 0, sizeof host_options);
  host_options.abi_version = EXT_HOST_ABI_VERSION;
  host_options.struct_size = sizeof host_options;
  host_options.descriptor = text;
  host_options.descriptor_len = (size_t)n;
  host_options.verify = verify_policy;
  host_options.authorize = authorize_policy;
  host_options.user = b;
  ext_backend_options selected = {sizeof selected, EXT_BACKEND_EXTENSION_CONTEXT, &backend,
                                  sizeof backend};
  status = ext_host_create(&host_options, &selected, &s->host);
  free(text); /* the engine copies the descriptor */
  if (status != EXT_OK) {
    if (backend.release) backend.release(backend.user);
    free_binding(env, b);
    free(s);
    throw_status(env, status);
    return 0;
  }
  return (jlong)(intptr_t)s;
}

static ext_host *host_of(jlong handle) { return ((session *)(intptr_t)handle)->host; }

JNIEXPORT void JNICALL Java_io_github_webong_ext_plugin_WamrNative_start(
    JNIEnv *env, jclass cls, jlong handle, jint timeout_ms) {
  (void)cls;
  ext_status status = ext_host_start(host_of(handle), (uint32_t)timeout_ms);
  if (status != EXT_OK) throw_status(env, status);
}

JNIEXPORT jbyteArray JNICALL Java_io_github_webong_ext_plugin_WamrNative_invoke(
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
  ext_status status = ext_host_invoke(host_of(handle), in, (size_t)n, (uint32_t)timeout_ms, &out);
  free(in);
  if (status != EXT_OK) {
    ext_buffer_free(&out);
    throw_status(env, status);
    return NULL;
  }
  jbyteArray result = to_java(env, out.data, out.len);
  ext_buffer_free(&out);
  return result;
}

JNIEXPORT void JNICALL Java_io_github_webong_ext_plugin_WamrNative_close(
    JNIEnv *env, jclass cls, jlong handle) {
  (void)env;
  (void)cls;
  ext_host_close(host_of(handle));
}

JNIEXPORT void JNICALL Java_io_github_webong_ext_plugin_WamrNative_destroy(
    JNIEnv *env, jclass cls, jlong handle) {
  (void)cls;
  session *s = (session *)(intptr_t)handle;
  if (!s) return;
  ext_host_destroy(s->host);
  free_binding(env, s->b);
  free(s);
}

JNIEXPORT jbyteArray JNICALL Java_io_github_webong_ext_plugin_WamrNative_inspect(
    JNIEnv *env, jclass cls, jbyteArray module, jint memory_pages, jint stack,
    jlong instruction_limit, jint timeout_ms) {
  (void)cls;
  ext_backend_extension_context backend;
  memset(&backend, 0, sizeof backend);
  ext_status status = make_backend(env, module, memory_pages, stack, instruction_limit, 0, NULL, &backend);
  if (status != EXT_OK) {
    throw_status(env, status);
    return NULL;
  }
  collector c = {0};
  ext_call_options call;
  memset(&call, 0, sizeof call);
  call.struct_size = sizeof call;
  call.timeout_ms = (uint32_t)timeout_ms;
  status = backend.connect ? backend.connect(backend.user, &call, collect, &c) : EXT_UNSUPPORTED;
  if (backend.close) backend.close(backend.user);
  if (backend.release) backend.release(backend.user);
  if (status != EXT_OK) {
    free(c.data);
    throw_status(env, status);
    return NULL;
  }
  jbyteArray result = to_java(env, c.data, c.len);
  free(c.data);
  return result;
}
