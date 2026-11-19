/* The guest registry: the shared cases (testdata/v1/registry-cases.json) run
 * against the relay guest they describe, which is this program started with
 * --guest <cases> <name>, then the configuration rules and routing under
 * concurrent registration. Usage: ext_registry_test <registry-cases.json> */
#define _XOPEN_SOURCE 700 /* realpath, under -std=c11 on glibc */
#include "sync.h"
#include "wire.h"
#include <assert.h>
#include <stdatomic.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#ifdef _WIN32
#include <fcntl.h>
#include <io.h>
#include <windows.h>
#else
#include <unistd.h>
#endif

static yyjson_doc *read_json(const char *path) {
  FILE *f = fopen(path, "rb");
  if (!f) {
    fprintf(stderr, "cannot open %s\n", path);
    exit(2);
  }
  fseek(f, 0, SEEK_END);
  long n = ftell(f);
  fseek(f, 0, SEEK_SET);
  char *text = malloc((size_t)n + 1);
  assert(text && fread(text, 1, (size_t)n, f) == (size_t)n);
  fclose(f);
  yyjson_doc *d = yyjson_read(text, (size_t)n, 0);
  free(text);
  assert(d);
  return d;
}
static yyjson_val *get(yyjson_val *v, const char *key) {
  return yyjson_obj_get(v, key);
}

/* ---- the relay guest ---- */

static char line[1 << 16], queued[1 << 16];
static char *next_line(void) {
  if (queued[0]) {
    snprintf(line, sizeof(line), "%s", queued);
    queued[0] = 0;
    return line;
  }
  return fgets(line, sizeof(line), stdin);
}
static void write_doc(yyjson_mut_doc *m) {
  char *text = yyjson_mut_write(m, 0, NULL);
  assert(text);
  fputs(text, stdout);
  fputc('\n', stdout);
  fflush(stdout);
  free(text);
}
static void sleep_ms(int ms) {
#ifdef _WIN32
  Sleep((DWORD)ms);
#else
  usleep((useconds_t)ms * 1000);
#endif
}
static void format_deadline(int64_t ms_from_now, char *out, size_t size) {
  struct timespec ts;
  timespec_get(&ts, TIME_UTC);
  int64_t wall = (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000 + ms_from_now;
  time_t seconds = (time_t)(wall / 1000);
  char stamp[32];
  strftime(stamp, sizeof(stamp), "%Y-%m-%dT%H:%M:%S", gmtime(&seconds));
  snprintf(out, size, "%s.%03dZ", stamp, (int)(wall % 1000));
}

static int guest(const char *cases_path, const char *name) {
#ifdef _WIN32
  _setmode(_fileno(stdin), _O_BINARY);
  _setmode(_fileno(stdout), _O_BINARY);
#endif
  yyjson_doc *cases = read_json(cases_path);
  yyjson_val *root = yyjson_doc_get_root(cases),
             *spec = get(root, "guest"),
             *contract = get(get(root, "guests"), name),
             *reverse = get(spec, "reverse");
  assert(contract);
  /* The descriptor. */
  yyjson_mut_doc *dm = yyjson_mut_doc_new(NULL);
  yyjson_mut_val *d = yyjson_mut_obj(dm), *identity = yyjson_mut_obj(dm),
                 *served = yyjson_mut_obj(dm), *ops = yyjson_mut_arr(dm),
                 *rev = yyjson_mut_obj(dm), *rops = yyjson_mut_arr(dm);
  char id[64];
  snprintf(id, sizeof(id), "registry/%s", name);
  yyjson_mut_obj_add_str(dm, d, "apiVersion", "ext.plugin/v1");
  yyjson_mut_obj_add_strcpy(dm, identity, "id", id);
  yyjson_mut_obj_add_str(dm, identity, "revision", "r1");
  yyjson_mut_obj_add_val(dm, d, "identity", identity);
  yyjson_mut_obj_add_strcpy(dm, served, "name", yyjson_get_str(contract));
  yyjson_mut_obj_add_str(dm, served, "version", "v1");
  const char *names[] = {"echo", "fail", "slow", "relay"};
  for (int i = 0; i < 4; i++) {
    yyjson_mut_val *op = yyjson_mut_obj(dm);
    yyjson_mut_obj_add_str(dm, op, "name", names[i]);
    yyjson_mut_arr_append(ops, op);
  }
  yyjson_mut_obj_add_val(dm, served, "operations", ops);
  yyjson_mut_val *contracts = yyjson_mut_arr(dm);
  yyjson_mut_arr_append(contracts, served);
  yyjson_mut_obj_add_val(dm, d, "contracts", contracts);
  yyjson_mut_obj_add_val(dm, rev, "name",
                         yyjson_val_mut_copy(dm, get(reverse, "name")));
  yyjson_mut_obj_add_val(dm, rev, "version",
                         yyjson_val_mut_copy(dm, get(reverse, "version")));
  size_t i, n;
  yyjson_val *v;
  yyjson_arr_foreach(get(reverse, "operations"), i, n, v) {
    yyjson_mut_val *op = yyjson_mut_obj(dm);
    yyjson_mut_obj_add_val(dm, op, "name", yyjson_val_mut_copy(dm, v));
    yyjson_mut_arr_append(rops, op);
  }
  yyjson_mut_obj_add_val(dm, rev, "operations", rops);
  yyjson_mut_val *reverses = yyjson_mut_arr(dm);
  yyjson_mut_arr_append(reverses, rev);
  yyjson_mut_obj_add_val(dm, d, "reverse", reverses);

  if (!fgets(line, sizeof(line), stdin))
    return 0;
  yyjson_mut_doc *hello = yyjson_mut_doc_new(NULL);
  yyjson_mut_val *h = yyjson_mut_obj(hello);
  yyjson_mut_doc_set_root(hello, h);
  yyjson_mut_obj_add_str(hello, h, "apiVersion", "ext.plugin/v1");
  yyjson_mut_obj_add_str(hello, h, "id", "hello");
  yyjson_mut_obj_add_val(hello, h, "payload", yyjson_mut_val_mut_copy(hello, d));
  write_doc(hello);
  yyjson_mut_doc_free(hello);
  yyjson_mut_doc_free(dm);

  unsigned next = 0;
  char *text;
  while ((text = next_line())) {
    yyjson_doc *rd = yyjson_read(text, strlen(text), 0);
    yyjson_val *req = yyjson_doc_get_root(rd);
    if (!req || get(req, "frame")) {
      yyjson_doc_free(rd);
      continue;
    }
    const char *op = yyjson_get_str(get(req, "operation"));
    yyjson_val *payload = get(req, "payload");
    yyjson_mut_doc *m = yyjson_mut_doc_new(NULL);
    yyjson_mut_val *resp = yyjson_mut_obj(m);
    yyjson_mut_doc_set_root(m, resp);
    yyjson_mut_obj_add_str(m, resp, "apiVersion", "ext.plugin/v1");
    yyjson_mut_obj_add_val(m, resp, "id", yyjson_val_mut_copy(m, get(req, "id")));
    yyjson_doc *answer_doc = NULL;
    if (!strcmp(op, "echo")) {
      yyjson_mut_obj_add_val(m, resp, "payload",
                             payload ? yyjson_val_mut_copy(m, payload)
                                     : yyjson_mut_null(m));
    } else if (!strcmp(op, "fail")) {
      yyjson_mut_obj_add_val(m, resp, "error",
                             yyjson_val_mut_copy(m, get(spec, "failError")));
    } else if (!strcmp(op, "slow")) {
      sleep_ms((int)yyjson_get_int(get(payload, "ms")));
      yyjson_mut_val *out = yyjson_mut_obj(m);
      yyjson_mut_obj_add_bool(m, out, "slept", true);
      yyjson_mut_obj_add_val(m, resp, "payload", out);
    } else { /* relay */
      yyjson_mut_doc *fm = yyjson_mut_doc_new(NULL);
      yyjson_mut_val *frame = yyjson_mut_obj(fm);
      yyjson_mut_doc_set_root(fm, frame);
      char rid[32], deadline[48];
      snprintf(rid, sizeof(rid), "r%u", ++next);
      yyjson_mut_obj_add_str(fm, frame, "frame", "reverse_request");
      yyjson_mut_obj_add_str(fm, frame, "apiVersion", "ext.plugin/v1");
      yyjson_mut_obj_add_strcpy(fm, frame, "id", rid);
      if (!yyjson_equals_str(get(payload, "scope"), "session"))
        yyjson_mut_obj_add_val(fm, frame, "parent",
                               yyjson_val_mut_copy(fm, get(req, "id")));
      yyjson_mut_val *ref = yyjson_mut_obj(fm);
      yyjson_mut_obj_add_val(fm, ref, "name",
                             yyjson_val_mut_copy(fm, get(reverse, "name")));
      yyjson_mut_obj_add_val(fm, ref, "version",
                             yyjson_val_mut_copy(fm, get(reverse, "version")));
      yyjson_mut_obj_add_val(fm, frame, "contract", ref);
      yyjson_mut_obj_add_val(fm, frame, "operation",
                             yyjson_val_mut_copy(fm, get(payload, "operation")));
      if (get(payload, "deadlineMs")) {
        format_deadline(yyjson_get_int(get(payload, "deadlineMs")), deadline,
                        sizeof(deadline));
        yyjson_mut_obj_add_strcpy(fm, frame, "deadline", deadline);
      } else {
        yyjson_mut_obj_add_val(fm, frame, "deadline",
                               yyjson_val_mut_copy(fm, get(req, "deadline")));
      }
      if (get(payload, "payload"))
        yyjson_mut_obj_add_val(fm, frame, "payload",
                               yyjson_val_mut_copy(fm, get(payload, "payload")));
      write_doc(fm);
      yyjson_mut_doc_free(fm);
      /* Anything but the answer overtook it and is served afterwards. */
      char reply[1 << 16];
      for (;;) {
        if (!fgets(reply, sizeof(reply), stdin))
          return 0;
        if (strstr(reply, "\"reverse_response\""))
          break;
        snprintf(queued, sizeof(queued), "%s", reply);
      }
      answer_doc = yyjson_read(reply, strlen(reply), 0);
      yyjson_val *a = yyjson_doc_get_root(answer_doc), *e = get(a, "error");
      yyjson_mut_val *out = yyjson_mut_obj(m);
      if (e)
        yyjson_mut_obj_add_val(m, out, "error", yyjson_val_mut_copy(m, e));
      else
        yyjson_mut_obj_add_val(m, out, "payload",
                               get(a, "payload")
                                   ? yyjson_val_mut_copy(m, get(a, "payload"))
                                   : yyjson_mut_null(m));
      yyjson_mut_obj_add_val(m, resp, "payload", out);
    }
    write_doc(m);
    yyjson_mut_doc_free(m);
    yyjson_doc_free(answer_doc);
    yyjson_doc_free(rd);
  }
  yyjson_doc_free(cases);
  return 0;
}

/* ---- the host side ---- */

static char self[4096], cases_path[4096];
typedef struct {
  const char *name, *contract;
  int deny_forward, deny_reverse;
  ext_host *host;
} guest_host;
static int32_t verify(void *user, const uint8_t *p, size_t n) {
  (void)user, (void)p, (void)n;
  return 0;
}
static int32_t forward_policy(void *user, const uint8_t *p, size_t n) {
  (void)p, (void)n;
  return ((guest_host *)user)->deny_forward;
}
static int32_t reverse_policy(void *user, const ext_call_options *o,
                              const uint8_t *p, size_t n) {
  (void)o, (void)p, (void)n;
  return ((guest_host *)user)->deny_reverse;
}
/* Creates, attaches and starts the host of one guest. */
static void open_guest(ext_registry *r, guest_host *g) {
  const char *args[] = {"--guest", cases_path, g->name};
  ext_jsonline_process_launch launch = {sizeof(launch), self, args, 3,
                                        NULL,           0,    NULL, 0};
  ext_backend_options backend = {sizeof(backend),
                                 EXT_BACKEND_JSONLINE_PROCESS_LAUNCH, &launch,
                                 sizeof(launch)};
  /* The descriptor is what the guest answers with: the same program prints it. */
  char descriptor[8192];
  snprintf(descriptor, sizeof(descriptor),
           "{\"apiVersion\":\"ext.plugin/v1\",\"identity\":{\"id\":"
           "\"registry/%s\",\"revision\":\"r1\"},\"contracts\":[{\"name\":\"%s\","
           "\"version\":\"v1\",\"operations\":[{\"name\":\"echo\"},{\"name\":"
           "\"fail\"},{\"name\":\"slow\"},{\"name\":\"relay\"}]}],\"reverse\":"
           "[{\"name\":\"registry.ask\",\"version\":\"v1\",\"operations\":[",
           g->name, g->contract);
  yyjson_doc *cases = read_json(cases_path);
  size_t i, n;
  yyjson_val *v;
  yyjson_arr_foreach(get(get(get(yyjson_doc_get_root(cases), "guest"), "reverse"),
                         "operations"),
                     i, n, v) {
    size_t at = strlen(descriptor);
    snprintf(descriptor + at, sizeof(descriptor) - at, "%s{\"name\":\"%s\"}",
             i ? "," : "", yyjson_get_str(v));
  }
  yyjson_doc_free(cases);
  strcat(descriptor, "]}]}");
  ext_host_options options = {EXT_HOST_ABI_VERSION,
                              sizeof(options),
                              (const uint8_t *)descriptor,
                              strlen(descriptor),
                              verify,
                              forward_policy,
                              g};
  assert(ext_host_create(&options, &backend, &g->host) == EXT_OK);
  ext_host_services services = {sizeof(services), g, reverse_policy, NULL,
                                0, 0, 0, NULL};
  assert(ext_registry_attach(r, g->host, &services) == EXT_OK);
  ext_status s = ext_host_start(g->host, 10000);
  if (s)
    fprintf(stderr, "start %s: %s\n", g->name, ext_host_status_string(s));
  assert(s == EXT_OK);
}
/* Calls a guest's operation and returns the response's payload document. */
static yyjson_doc *call(guest_host *g, const char *operation,
                        yyjson_val *payload, uint32_t timeout_ms) {
  yyjson_mut_doc *m = yyjson_mut_doc_new(NULL);
  yyjson_mut_val *root = yyjson_mut_obj(m), *ref = yyjson_mut_obj(m);
  yyjson_mut_doc_set_root(m, root);
  yyjson_mut_obj_add_str(m, ref, "name", g->contract);
  yyjson_mut_obj_add_str(m, ref, "version", "v1");
  yyjson_mut_obj_add_val(m, root, "contract", ref);
  yyjson_mut_obj_add_str(m, root, "operation", operation);
  yyjson_mut_obj_add_val(m, root, "payload",
                         payload ? yyjson_val_mut_copy(m, payload)
                                 : yyjson_mut_null(m));
  size_t n = 0;
  char *input = yyjson_mut_write(m, 0, &n);
  yyjson_mut_doc_free(m);
  ext_call_options options = {sizeof(options), timeout_ms, NULL, NULL};
  ext_buffer out = {0};
  ext_status s = ext_host_call(g->host, (uint8_t *)input, n, &options, &out);
  free(input);
  if (s) {
    fprintf(stderr, "call %s.%s: %s\n", g->name, operation,
            ext_host_status_string(s));
    return NULL;
  }
  yyjson_doc *d = yyjson_read((char *)out.data, out.len, 0);
  ext_buffer_free(&out);
  return d;
}
static int listed(yyjson_val *names, const char *name) {
  size_t i, n;
  yyjson_val *v;
  yyjson_arr_foreach(names, i, n, v) if (yyjson_equals_str(v, name)) return 1;
  return 0;
}
static void route_all(ext_registry *r, yyjson_val *edges) {
  size_t i, n;
  yyjson_val *e;
  yyjson_arr_foreach(edges, i, n, e) {
    yyjson_val *f = get(e, "from"), *t = get(e, "to");
    ext_registry_edge edge = {sizeof(edge),
                              yyjson_get_str(get(f, "name")),
                              yyjson_get_str(get(f, "version")),
                              yyjson_get_str(get(f, "operation")),
                              yyjson_get_str(get(t, "name")),
                              yyjson_get_str(get(t, "version")),
                              yyjson_get_str(get(t, "operation"))};
    assert(ext_registry_route(r, &edge) == EXT_OK);
  }
}

#define MAX_GUESTS 8
static int run_cases(void) {
  yyjson_doc *doc = read_json(cases_path);
  yyjson_val *root = yyjson_doc_get_root(doc), *guests = get(root, "guests");
  size_t ci, cn, failures = 0;
  yyjson_val *c;
  yyjson_arr_foreach(get(root, "cases"), ci, cn, c) {
    const char *name = yyjson_get_str(get(c, "name"));
    ext_registry *r = NULL;
    assert(ext_registry_create(&r) == EXT_OK);
    guest_host hosts[MAX_GUESTS] = {0};
    size_t count = 0, gi, gn;
    yyjson_val *gk, *gv;
    yyjson_obj_foreach(guests, gi, gn, gk, gv) {
      guest_host *g = &hosts[count++];
      g->name = yyjson_get_str(gk);
      g->contract = yyjson_get_str(gv);
      g->deny_forward = listed(get(c, "forwardDeny"), g->name);
      g->deny_reverse = listed(get(c, "reverseDeny"), g->name);
      open_guest(r, g);
      assert(ext_registry_register(r, g->contract, "v1", g->host) == EXT_OK);
    }
    route_all(r, get(root, "edges"));
    size_t si, sn;
    yyjson_val *s;
    yyjson_arr_foreach(get(c, "steps"), si, sn, s) {
      guest_host *g = NULL;
      const char *who = yyjson_get_str(get(s, "call"));
      if (!who)
        who = yyjson_get_str(get(s, "unregister"));
      for (size_t k = 0; k < count; k++)
        if (!strcmp(hosts[k].name, who))
          g = &hosts[k];
      assert(g);
      if (get(s, "unregister")) {
        assert(ext_registry_unregister(r, g->contract, "v1") == EXT_OK);
        continue;
      }
      yyjson_val *timeout = get(s, "timeoutMs");
      yyjson_doc *got =
          call(g, yyjson_get_str(get(s, "operation")), get(s, "payload"),
               timeout ? (uint32_t)yyjson_get_uint(timeout) : 5000);
      yyjson_val *response = got ? yyjson_doc_get_root(got) : NULL,
                 *payload = get(response, "payload");
      int ok = 0;
      if (get(s, "result"))
        ok = payload && yyjson_equals(payload, get(s, "result"));
      else
        ok = payload && listed(get(s, "resultError"),
                               yyjson_get_str(get(get(payload, "error"), "code")));
      if (!ok) {
        char *text = response ? yyjson_val_write(response, 0, NULL) : NULL;
        fprintf(stderr, "case %s, step %zu: got %s\n", name, si,
                text ? text : "(no response)");
        free(text);
        failures++;
      }
      yyjson_doc_free(got);
    }
    for (size_t k = 0; k < count; k++)
      ext_host_destroy(hosts[k].host);
    ext_registry_destroy(r);
  }
  yyjson_doc_free(doc);
  return (int)failures;
}

/* ---- configuration ---- */

static void test_configuration(void) {
  ext_registry *r = NULL;
  assert(ext_registry_create(NULL) == EXT_INVALID);
  assert(ext_registry_create(&r) == EXT_OK);
  guest_host b = {"b", "registry.b", 0, 0, NULL};
  open_guest(r, &b);
  assert(ext_registry_register(r, "registry.b", "v1", NULL) == EXT_INVALID);
  assert(ext_registry_register(r, "Not A Name", "v1", b.host) == EXT_INVALID);
  assert(ext_registry_register(r, "registry.b", "v 1", b.host) == EXT_INVALID);
  assert(ext_registry_register(r, "registry.other", "v1", b.host) ==
         EXT_UNSUPPORTED);
  assert(ext_registry_register(r, "registry.b", "v2", b.host) ==
         EXT_UNSUPPORTED);
  assert(ext_registry_register(r, "registry.b", "v1", b.host) == EXT_OK);
  assert(ext_registry_register(r, "registry.b", "v1", b.host) == EXT_EXISTS);
  assert(ext_registry_unregister(r, "registry.x", "v1") == EXT_NOT_FOUND);
  ext_registry_edge e = {sizeof(e),        "registry.ask", "v1", "echo-b",
                         "registry.b",     "v1",           "echo"};
  assert(ext_registry_route(r, &e) == EXT_OK);
  assert(ext_registry_route(r, &e) == EXT_EXISTS);
  ext_registry_edge bad = e;
  bad.to_operation = "Bad Name";
  bad.from_operation = "other";
  assert(ext_registry_route(r, &bad) == EXT_INVALID);
  bad = e;
  bad.struct_size = 1;
  assert(ext_registry_route(r, &bad) == EXT_INVALID);
  assert(ext_registry_route(r, NULL) == EXT_INVALID);
  /* attach follows ext_host_set_services, and the registry is the handler. */
  ext_host_services s = {sizeof(s), &b, reverse_policy, NULL, 0, 0, 0, NULL};
  assert(ext_registry_attach(r, b.host, &s) == EXT_INVALID); /* started */
  ext_host_services own = s;
  own.handle = (ext_reverse_handler)1;
  assert(ext_registry_attach(r, b.host, &own) == EXT_INVALID);
  ext_host_services none = s;
  none.authorize = NULL;
  assert(ext_registry_attach(r, b.host, &none) == EXT_DENIED);
  assert(ext_registry_attach(NULL, b.host, &s) == EXT_INVALID);
  assert(ext_registry_unregister(r, "registry.b", "v1") == EXT_OK);
  assert(ext_registry_unregister(r, "registry.b", "v1") == EXT_NOT_FOUND);
  ext_host_destroy(b.host);
  ext_registry_destroy(r);
  ext_registry_destroy(NULL);
}

/* ---- concurrency ---- */

#define SOURCES 4
#define ROUNDS 25
static ext_registry *shared;
static guest_host sources[SOURCES];
static atomic_int answered, refused;
static void *caller(void *arg) {
  guest_host *g = arg;
  for (int i = 0; i < ROUNDS; i++) {
    char text[96];
    snprintf(text, sizeof(text), "{\"operation\":\"echo-b\",\"payload\":%d}", i);
    yyjson_doc *payload = yyjson_read(text, strlen(text), 0);
    yyjson_doc *got = call(g, "relay", yyjson_doc_get_root(payload), 10000);
    assert(got);
    yyjson_val *out = get(yyjson_doc_get_root(got), "payload");
    if (get(out, "payload")) {
      assert(yyjson_get_int(get(out, "payload")) == i);
      atomic_fetch_add(&answered, 1);
    } else {
      assert(yyjson_equals_str(get(get(out, "error"), "code"),
                               "reverse_unavailable"));
      atomic_fetch_add(&refused, 1);
    }
    yyjson_doc_free(got);
    yyjson_doc_free(payload);
  }
  return NULL;
}
/* Sources call through the registry while the target is registered and
 * unregistered under them, and edges are added: every call is answered or
 * refused, never lost, and unregister waits for the calls in flight. */
static void test_threads(void) {
  assert(ext_registry_create(&shared) == EXT_OK);
  guest_host b = {"b", "registry.b", 0, 0, NULL};
  open_guest(shared, &b);
  ext_registry_edge e = {sizeof(e),    "registry.ask", "v1", "echo-b",
                         "registry.b", "v1",           "echo"};
  assert(ext_registry_route(shared, &e) == EXT_OK);
  assert(ext_registry_register(shared, "registry.b", "v1", b.host) == EXT_OK);
  pthread_t threads[SOURCES];
  for (int i = 0; i < SOURCES; i++) {
    sources[i] = (guest_host){"a", "registry.a", 0, 0, NULL};
    open_guest(shared, &sources[i]);
  }
  for (int i = 0; i < SOURCES; i++)
    assert(pthread_create(&threads[i], NULL, caller, &sources[i]) == 0);
  for (int i = 0; i < 20; i++) {
    char op[32];
    snprintf(op, sizeof(op), "extra-%d", i);
    ext_registry_edge x = {sizeof(x),    "registry.ask", "v1", op,
                           "registry.b", "v1",           "echo"};
    assert(ext_registry_route(shared, &x) == EXT_OK);
    sleep_ms(5);
    assert(ext_registry_unregister(shared, "registry.b", "v1") == EXT_OK);
    sleep_ms(5);
    assert(ext_registry_register(shared, "registry.b", "v1", b.host) ==
           EXT_OK);
  }
  for (int i = 0; i < SOURCES; i++)
    pthread_join(threads[i], NULL);
  assert(atomic_load(&answered) + atomic_load(&refused) == SOURCES * ROUNDS);
  assert(atomic_load(&answered) > 0);
  for (int i = 0; i < SOURCES; i++)
    ext_host_destroy(sources[i].host);
  assert(ext_registry_unregister(shared, "registry.b", "v1") == EXT_OK);
  ext_host_destroy(b.host);
  ext_registry_destroy(shared);
}

#ifdef _WIN32
static void find_self(const char *argv0) {
  (void)argv0;
  assert(GetModuleFileNameA(NULL, self, sizeof(self)) > 0);
}
static void find_cases(const char *path) {
  assert(GetFullPathNameA(path, sizeof(cases_path), cases_path, NULL) > 0);
}
#else
static void find_self(const char *argv0) { assert(realpath(argv0, self)); }
static void find_cases(const char *path) { assert(realpath(path, cases_path)); }
#endif

int main(int argc, char **argv) {
  if (argc == 4 && !strcmp(argv[1], "--guest"))
    return guest(argv[2], argv[3]);
  if (argc != 2) {
    fprintf(stderr, "usage: %s <registry-cases.json>\n", argv[0]);
    return 2;
  }
  find_self(argv[0]);
  find_cases(argv[1]);
  int failures = run_cases();
  if (failures) {
    fprintf(stderr, "%d registry case step(s) failed\n", failures);
    return 1;
  }
  test_configuration();
  test_threads();
  puts("guest registry checks passed");
  return 0;
}
