/* ext-ledger: the graph from any shell. Reads go through a reader, which never
 * locks or writes; register and apply open the store. Output is canonical JSON. */
#include "ext_ledger.h"

#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define EXIT_USAGE 64

static const char usage[] =
    "usage: ext-ledger <command> <dir> [arguments]\n"
    "\n"
    "  snapshot DIR NS\n"
    "  get-vertex DIR NS ID\n"
    "  get-edge DIR NS ID\n"
    "  query-vertices DIR QUERY\n"
    "  query-edges DIR QUERY\n"
    "  traverse DIR NS START [--depth N] [--max N]\n"
    "  changes DIR [NS] [--after CURSOR] [--limit N]\n"
    "  watch DIR [NS] [--after CURSOR] [--buffer N]   one change per line until the watch ends\n"
    "  register DIR SCHEMA\n"
    "  apply DIR TRANSACTION\n"
    "  fingerprint TRANSACTION\n"
    "\n"
    "QUERY, SCHEMA and TRANSACTION are JSON in the wire form of the res/graph store\n"
    "contract; - reads it from standard input. A failure prints {\"error\":\"<code>\"} on\n"
    "standard error and exits with the code's number (" "64 for a usage error).\n";

static ext_ledger_context ctx;

static void on_signal(int sig) {
  (void)sig;
  ctx.canceled = 1;
}

static int fail(int code) {
  fprintf(stderr, "{\"error\":\"%s\"}\n", ext_ledger_code_name(code));
  return code;
}

static int bad_usage(const char *why) {
  fprintf(stderr, "ext-ledger: %s\n\n%s", why, usage);
  return EXIT_USAGE;
}

static char *read_stdin(size_t *len) {
  size_t cap = 4096, n = 0;
  char *buf = malloc(cap);
  if (!buf) return NULL;
  size_t got;
  while ((got = fread(buf + n, 1, cap - n, stdin)) > 0) {
    n += got;
    if (n == cap) {
      char *p = realloc(buf, cap *= 2);
      if (!p) {
        free(buf);
        return NULL;
      }
      buf = p;
    }
  }
  *len = n;
  return buf;
}

/* The JSON argument: the text itself, or standard input for "-". */
static const char *json_arg(const char *arg, size_t *len, char **owned) {
  *owned = NULL;
  if (strcmp(arg, "-") == 0) {
    *owned = read_stdin(len);
    return *owned;
  }
  *len = strlen(arg);
  return arg;
}

static int print(int rc, char *out, size_t len) {
  if (rc) return fail(rc);
  fwrite(out, 1, len, stdout);
  fputc('\n', stdout);
  ext_ledger_free(out);
  return fflush(stdout) ? EXT_LEDGER_IO : 0;
}

typedef struct {
  const char *positional[4];
  int npositional;
  int64_t after, limit, buffer, depth, max;
} args;

static int parse_args(int argc, char **argv, args *a) {
  memset(a, 0, sizeof *a);
  a->limit = a->buffer = a->max = 1000;
  a->depth = 8;
  for (int i = 0; i < argc; i++) {
    int64_t *flag = NULL;
    if (strcmp(argv[i], "--after") == 0) flag = &a->after;
    else if (strcmp(argv[i], "--limit") == 0) flag = &a->limit;
    else if (strcmp(argv[i], "--buffer") == 0) flag = &a->buffer;
    else if (strcmp(argv[i], "--depth") == 0) flag = &a->depth;
    else if (strcmp(argv[i], "--max") == 0) flag = &a->max;
    if (flag) {
      char *end;
      if (i + 1 >= argc) return -1;
      *flag = strtoll(argv[++i], &end, 10);
      if (*end || end == argv[i]) return -1;
      continue;
    }
    if (a->npositional == 4) return -1;
    a->positional[a->npositional++] = argv[i];
  }
  return 0;
}

int main(int argc, char **argv) {
  if (argc < 2) return bad_usage("no command");
  const char *command = argv[1];
  if (strcmp(command, "-h") == 0 || strcmp(command, "--help") == 0 || strcmp(command, "help") == 0) {
    fputs(usage, stdout);
    return 0;
  }
  signal(SIGINT, on_signal);
#ifdef SIGTERM
  signal(SIGTERM, on_signal);
#endif
  args a;
  if (parse_args(argc - 2, argv + 2, &a)) return bad_usage("bad arguments");
  const char **p = a.positional;
  int n = a.npositional;

  if (strcmp(command, "fingerprint") == 0) {
    if (n != 1) return bad_usage("fingerprint takes TRANSACTION");
    size_t len;
    char *owned, *canonical, fp[65];
    const char *text = json_arg(p[0], &len, &owned);
    if (!text) return fail(EXT_LEDGER_IO);
    size_t clen;
    int rc = ext_ledger_canonical_transaction(text, len, &canonical, &clen, fp);
    free(owned);
    if (rc) return fail(rc);
    printf("{\"canonical\":");
    /* canonical is JSON already; print it as a JSON string by escaping it */
    putchar('"');
    for (size_t i = 0; i < clen; i++) {
      if (canonical[i] == '"' || canonical[i] == '\\') putchar('\\');
      putchar(canonical[i]);
    }
    printf("\",\"sha256\":\"%s\"}\n", fp);
    ext_ledger_free(canonical);
    return 0;
  }

  if (n < 1) return bad_usage("no store directory");
  const char *dir = p[0];
  int writes = strcmp(command, "register") == 0 || strcmp(command, "apply") == 0;
  ext_ledger *store;
  int rc = writes ? ext_ledger_open(&ctx, dir, NULL, &store) : ext_ledger_open_reader(&ctx, dir, NULL, &store);
  if (rc) return fail(rc);
  char *out = NULL;
  size_t len = 0;

  if (strcmp(command, "snapshot") == 0 && n == 2) {
    rc = print(ext_ledger_snapshot(store, &ctx, p[1], &out, &len), out, len);
  } else if ((strcmp(command, "get-vertex") == 0 || strcmp(command, "get-edge") == 0) && n == 3) {
    rc = command[4] == 'v' ? ext_ledger_get_vertex(store, &ctx, p[1], p[2], &out, &len) : ext_ledger_get_edge(store, &ctx, p[1], p[2], &out, &len);
    rc = print(rc, out, len);
  } else if ((strcmp(command, "query-vertices") == 0 || strcmp(command, "query-edges") == 0 || writes) && n == 2) {
    size_t tlen;
    char *owned;
    const char *text = json_arg(p[1], &tlen, &owned);
    if (!text) rc = fail(EXT_LEDGER_IO);
    else if (strcmp(command, "register") == 0) {
      rc = ext_ledger_register(store, &ctx, text, tlen);
      rc = rc ? fail(rc) : 0;
    } else if (strcmp(command, "apply") == 0) {
      rc = print(ext_ledger_apply(store, &ctx, text, tlen, &out, &len), out, len);
    } else if (command[6] == 'v') {
      rc = print(ext_ledger_query_vertices(store, &ctx, text, tlen, &out, &len), out, len);
    } else {
      rc = print(ext_ledger_query_edges(store, &ctx, text, tlen, &out, &len), out, len);
    }
    free(owned);
  } else if (strcmp(command, "traverse") == 0 && n == 3) {
    rc = print(ext_ledger_traverse(store, &ctx, p[1], p[2], a.depth, a.max, &out, &len), out, len);
  } else if (strcmp(command, "changes") == 0 && n <= 2) {
    rc = print(ext_ledger_changes(store, &ctx, n == 2 ? p[1] : "", (uint64_t)a.after, a.limit, &out, &len), out, len);
  } else if (strcmp(command, "watch") == 0 && n <= 2) {
    ext_ledger_watch *w;
    rc = ext_ledger_watch_open(store, &ctx, n == 2 ? p[1] : "", (uint64_t)a.after, a.buffer, &w);
    if (rc) {
      rc = fail(rc);
    } else {
      for (;;) {
        rc = ext_ledger_watch_next(w, 200, &out, &len);
        if (rc == EXT_LEDGER_TIMEOUT) {
          if (ctx.canceled) {
            rc = 0;
            break;
          }
          continue;
        }
        if (rc == EXT_LEDGER_UNAVAILABLE) { /* the watch ended: resume from the last cursor printed */
          rc = fail(rc);
          break;
        }
        if ((rc = print(rc, out, len))) break;
      }
      ext_ledger_watch_close(w);
    }
  } else {
    ext_ledger_close(store);
    return bad_usage("unknown command or wrong arguments");
  }
  ext_ledger_close(store);
  return rc;
}
