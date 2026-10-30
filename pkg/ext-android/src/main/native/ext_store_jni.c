/* JNI binding for the package store of the shared C engine: ext_store_* and the read-only
 * ext_layered_* view. Paths and ids arrive as UTF-8 bytes rather than as jstring, whose
 * modified UTF-8 would spell a supplementary character differently from the file system.
 * The engine serializes the calls on one store; the Java classes keep a handle alive for
 * as long as a call is in it. */
#include <jni.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "ext_host.h"

#define CLASS "io/github/webong/ext/plugin/"

/* The kinds of whole-root answer, as NativeEngine names them. */
enum { REPORT_IDS, REPORT_LIST, REPORT_VERIFY, REPORT_UNVERIFIED };
/* How a package is put into a store, as NativeEngine names them. */
enum { PUT_INSTALL, PUT_UPGRADE, PUT_REPLACE };

static void throw_status(JNIEnv *env, ext_status status) {
  jclass cls = (*env)->FindClass(env, CLASS "GuestException");
  if (!cls) return;
  jmethodID ctor = (*env)->GetMethodID(env, cls, "<init>", "(I)V");
  if (!ctor) return;
  jobject e = (*env)->NewObject(env, cls, ctor, (jint)status);
  if (e) (*env)->Throw(env, (jthrowable)e);
}

/* A NUL-terminated copy of the bytes, or NULL with an exception pending: a NUL inside would
 * end the path early and name something else, so it is invalid. */
static char *c_string(JNIEnv *env, jbyteArray text) {
  jsize n = (*env)->GetArrayLength(env, text);
  char *copy = malloc((size_t)n + 1);
  if (!copy) {
    throw_status(env, EXT_NOMEM);
    return NULL;
  }
  if (n) (*env)->GetByteArrayRegion(env, text, 0, n, (jbyte *)copy);
  copy[n] = 0;
  if (strlen(copy) != (size_t)n) {
    free(copy);
    throw_status(env, EXT_INVALID);
    return NULL;
  }
  return copy;
}

/* The engine's answer as a Java array; frees it. A failure throws and returns NULL. */
static jbyteArray take(JNIEnv *env, ext_status status, ext_buffer *out) {
  jbyteArray result = NULL;
  if (status != EXT_OK) {
    throw_status(env, status);
  } else if (out->len > INT32_MAX) {
    throw_status(env, EXT_NOMEM);
  } else {
    result = (*env)->NewByteArray(env, (jsize)out->len);
    if (result && out->len) {
      (*env)->SetByteArrayRegion(env, result, 0, (jsize)out->len, (const jbyte *)out->data);
    }
  }
  ext_buffer_free(out);
  return result;
}

static ext_store_limits limits_of(jlong max_artifact_bytes, jint max_tree_files, jlong max_tree_bytes) {
  ext_store_limits limits = {(uint64_t)max_artifact_bytes, (uint32_t)max_tree_files,
                             (uint64_t)max_tree_bytes};
  return limits;
}

JNIEXPORT jlong JNICALL Java_io_github_webong_ext_plugin_NativeEngine_storeOpen(
    JNIEnv *env, jclass cls, jbyteArray root, jlong max_artifact_bytes, jint max_tree_files,
    jlong max_tree_bytes) {
  (void)cls;
  char *path = c_string(env, root);
  if (!path) return 0;
  ext_store_limits limits = limits_of(max_artifact_bytes, max_tree_files, max_tree_bytes);
  ext_store *store = NULL;
  ext_status status = ext_store_open(path, &limits, &store);
  free(path);
  if (status != EXT_OK) {
    throw_status(env, status);
    return 0;
  }
  return (jlong)(intptr_t)store;
}

JNIEXPORT void JNICALL Java_io_github_webong_ext_plugin_NativeEngine_storeClose(
    JNIEnv *env, jclass cls, jlong handle) {
  (void)env;
  (void)cls;
  if (handle) ext_store_close((ext_store *)(intptr_t)handle);
}

JNIEXPORT jbyteArray JNICALL Java_io_github_webong_ext_plugin_NativeEngine_storePut(
    JNIEnv *env, jclass cls, jlong handle, jint how, jbyteArray source, jint flags) {
  (void)cls;
  char *path = c_string(env, source);
  if (!path) return NULL;
  ext_store *store = (ext_store *)(intptr_t)handle;
  ext_buffer manifest = {0};
  ext_status status;
  switch (how) {
    case PUT_INSTALL: status = ext_store_install(store, path, (uint32_t)flags, &manifest); break;
    case PUT_UPGRADE: status = ext_store_upgrade(store, path, (uint32_t)flags, &manifest); break;
    case PUT_REPLACE: status = ext_store_replace(store, path, (uint32_t)flags, &manifest); break;
    default: status = EXT_INVALID; break;
  }
  free(path);
  return take(env, status, &manifest);
}

JNIEXPORT void JNICALL Java_io_github_webong_ext_plugin_NativeEngine_storeRemove(
    JNIEnv *env, jclass cls, jlong handle, jbyteArray id) {
  (void)cls;
  char *name = c_string(env, id);
  if (!name) return;
  ext_status status = ext_store_remove((ext_store *)(intptr_t)handle, name);
  free(name);
  if (status != EXT_OK) throw_status(env, status);
}

JNIEXPORT jbyteArray JNICALL Java_io_github_webong_ext_plugin_NativeEngine_storeLoad(
    JNIEnv *env, jclass cls, jlong handle, jbyteArray id) {
  (void)cls;
  char *name = c_string(env, id);
  if (!name) return NULL;
  ext_buffer manifest = {0};
  ext_status status = ext_store_load((ext_store *)(intptr_t)handle, name, &manifest);
  free(name);
  return take(env, status, &manifest);
}

JNIEXPORT jbyteArray JNICALL Java_io_github_webong_ext_plugin_NativeEngine_storeReport(
    JNIEnv *env, jclass cls, jlong handle, jint kind) {
  (void)cls;
  ext_store *store = (ext_store *)(intptr_t)handle;
  ext_buffer out = {0};
  ext_status status;
  switch (kind) {
    case REPORT_IDS: status = ext_store_ids(store, &out); break;
    case REPORT_LIST: status = ext_store_list(store, &out); break;
    case REPORT_VERIFY: status = ext_store_verify(store, &out); break;
    case REPORT_UNVERIFIED: status = ext_store_list_unverified(store, &out); break;
    default: status = EXT_INVALID; break;
  }
  return take(env, status, &out);
}

JNIEXPORT jlong JNICALL Java_io_github_webong_ext_plugin_NativeEngine_layeredOpen(
    JNIEnv *env, jclass cls, jobjectArray roots, jlong max_artifact_bytes, jint max_tree_files,
    jlong max_tree_bytes) {
  (void)cls;
  jsize count = (*env)->GetArrayLength(env, roots);
  /* At least one slot, so that a request for no roots still reaches the engine, which refuses it. */
  char **paths = calloc(count ? (size_t)count : 1, sizeof *paths);
  if (!paths) {
    throw_status(env, EXT_NOMEM);
    return 0;
  }
  jsize made = 0;
  for (; made < count; made++) {
    jbyteArray root = (jbyteArray)(*env)->GetObjectArrayElement(env, roots, made);
    if (!root) {
      if (!(*env)->ExceptionCheck(env)) throw_status(env, EXT_INVALID);
      break;
    }
    paths[made] = c_string(env, root);
    (*env)->DeleteLocalRef(env, root);
    if (!paths[made]) break;
  }
  ext_layered *layered = NULL;
  ext_status status = EXT_OK;
  int failed = made < count;
  if (!failed) {
    ext_store_limits limits = limits_of(max_artifact_bytes, max_tree_files, max_tree_bytes);
    status = ext_layered_open((const char *const *)paths, (size_t)count, &limits, &layered);
  }
  for (jsize i = 0; i < made; i++) free(paths[i]);
  free(paths);
  if (failed) return 0; /* the exception is pending */
  if (status != EXT_OK) {
    throw_status(env, status);
    return 0;
  }
  return (jlong)(intptr_t)layered;
}

JNIEXPORT void JNICALL Java_io_github_webong_ext_plugin_NativeEngine_layeredClose(
    JNIEnv *env, jclass cls, jlong handle) {
  (void)env;
  (void)cls;
  if (handle) ext_layered_close((ext_layered *)(intptr_t)handle);
}

/* layer[0] receives the position of the root that served the package. */
JNIEXPORT jbyteArray JNICALL Java_io_github_webong_ext_plugin_NativeEngine_layeredLoad(
    JNIEnv *env, jclass cls, jlong handle, jbyteArray id, jintArray layer) {
  (void)cls;
  char *name = c_string(env, id);
  if (!name) return NULL;
  ext_buffer manifest = {0};
  size_t position = 0;
  ext_status status = ext_layered_load((ext_layered *)(intptr_t)handle, name, &manifest, &position);
  free(name);
  if (status == EXT_OK) {
    jint at = position > INT32_MAX ? INT32_MAX : (jint)position;
    (*env)->SetIntArrayRegion(env, layer, 0, 1, &at);
  }
  return take(env, status, &manifest);
}

JNIEXPORT jbyteArray JNICALL Java_io_github_webong_ext_plugin_NativeEngine_layeredReport(
    JNIEnv *env, jclass cls, jlong handle, jint kind) {
  (void)cls;
  ext_layered *layered = (ext_layered *)(intptr_t)handle;
  ext_buffer out = {0};
  ext_status status;
  switch (kind) {
    case REPORT_LIST: status = ext_layered_list(layered, &out); break;
    case REPORT_VERIFY: status = ext_layered_verify(layered, &out); break;
    case REPORT_UNVERIFIED: status = ext_layered_list_unverified(layered, &out); break;
    default: status = EXT_INVALID; break;
  }
  return take(env, status, &out);
}
