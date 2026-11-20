/* ext ledger: the res/graph store in C.
 *
 * A generic, namespaced, directed graph store whose contract is the data in
 * res/graph/testdata/store/v1. Values cross this API as JSON in the wire form
 * of entities.schema.json; every JSON the library returns is canonical JSON.
 *
 * Backends:
 *   memory   ext_ledger_open_memory: the state of one process.
 *   file     ext_ledger_open: a directory holding an append-only log and a
 *            checkpoint (ledger.json), shared by processes through an advisory
 *            lock. It needs nothing beyond the C library and the OS.
 *            ext_ledger_open_reader reads one without ever writing.
 *   sqlite   ext_ledger_open_sqlite: one SQLite database laid out as
 *            tables.json says. Optional: a build without EXT_LEDGER_SQLITE
 *            answers EXT_LEDGER_UNSUPPORTED.
 *
 * A store handle may be used from several threads. Strings are UTF-8 and
 * carry no NUL unless a length is given. Every char* the library returns is
 * released with ext_ledger_free. */
#ifndef EXT_LEDGER_H
#define EXT_LEDGER_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#if defined(_WIN32) && !defined(EXT_LEDGER_STATIC)
#ifdef EXT_LEDGER_BUILD
#define EXT_LEDGER_API __declspec(dllexport)
#else
#define EXT_LEDGER_API __declspec(dllimport)
#endif
#elif defined(__GNUC__)
#define EXT_LEDGER_API __attribute__((visibility("default")))
#else
#define EXT_LEDGER_API
#endif

#define EXT_LEDGER_LAYOUT "ext.graph.ledger/v1"

/* The codes of res/system/testdata/codes.json that a store returns. */
typedef enum {
  EXT_LEDGER_OK = 0,
  EXT_LEDGER_INVALID = 1,
  EXT_LEDGER_NOT_FOUND = 2,
  EXT_LEDGER_CHANGED = 3,
  EXT_LEDGER_EXISTS = 4,
  EXT_LEDGER_DENIED = 5,
  EXT_LEDGER_TOO_LARGE = 6,
  EXT_LEDGER_EXPIRED = 7,
  EXT_LEDGER_UNSUPPORTED = 8,
  EXT_LEDGER_UNAVAILABLE = 9,
  EXT_LEDGER_BUSY = 10,
  EXT_LEDGER_CANCELED = 11,
  EXT_LEDGER_TIMEOUT = 12,
  EXT_LEDGER_IO = 13
} ext_ledger_code;

/* The contract name of a code ("invalid", "not_found", ...); "" for OK. */
EXT_LEDGER_API const char *ext_ledger_code_name(int code);

/* The bounds of rules.json. A value that is zero or negative takes the default. */
typedef struct {
  int64_t max_changes;
  int64_t max_change_bytes;
  int64_t max_transaction_records;
  int64_t max_record_bytes;
  int64_t max_results;
  int64_t max_snapshot_records;
} ext_ledger_options;

/* Cancellation and a deadline for one call; NULL means neither. Set canceled to
 * non-zero from any thread. deadline_unix_ms is milliseconds since the Unix
 * epoch, 0 for none. A canceled or expired context fails first. */
typedef struct {
  volatile int canceled;
  int64_t deadline_unix_ms;
} ext_ledger_context;

typedef struct ext_ledger ext_ledger;
typedef struct ext_ledger_watch ext_ledger_watch;

EXT_LEDGER_API int ext_ledger_open_memory(const ext_ledger_options *options, ext_ledger **out);
EXT_LEDGER_API int ext_ledger_open(const ext_ledger_context *ctx, const char *dir, const ext_ledger_options *options, ext_ledger **out);
/* A reader of a file store: it never locks, creates or writes anything, so it
 * also reads a directory it may not write (a read-only mount, another app's
 * shared container). Register and apply answer EXT_LEDGER_UNSUPPORTED; a
 * directory that holds no store answers EXT_LEDGER_NOT_FOUND. */
EXT_LEDGER_API int ext_ledger_open_reader(const ext_ledger_context *ctx, const char *dir, const ext_ledger_options *options, ext_ledger **out);
EXT_LEDGER_API int ext_ledger_open_sqlite(const ext_ledger_context *ctx, const char *path, const ext_ledger_options *options, ext_ledger **out);
/* Ends every watch and releases the store. Safe on NULL. */
EXT_LEDGER_API void ext_ledger_close(ext_ledger *store);

/* schema: the schema entity. */
EXT_LEDGER_API int ext_ledger_register(ext_ledger *store, const ext_ledger_context *ctx, const char *schema, size_t len);
/* transaction: the transaction entity; *commit receives the commit entity. */
EXT_LEDGER_API int ext_ledger_apply(ext_ledger *store, const ext_ledger_context *ctx, const char *transaction, size_t len, char **commit, size_t *commit_len);
EXT_LEDGER_API int ext_ledger_get_vertex(ext_ledger *store, const ext_ledger_context *ctx, const char *ns, const char *id, char **out, size_t *out_len);
EXT_LEDGER_API int ext_ledger_get_edge(ext_ledger *store, const ext_ledger_context *ctx, const char *ns, const char *id, char **out, size_t *out_len);
/* query: the vertex or edge query entity; *out receives a JSON array in ID order. */
EXT_LEDGER_API int ext_ledger_query_vertices(ext_ledger *store, const ext_ledger_context *ctx, const char *query, size_t len, char **out, size_t *out_len);
EXT_LEDGER_API int ext_ledger_query_edges(ext_ledger *store, const ext_ledger_context *ctx, const char *query, size_t len, char **out, size_t *out_len);
/* *out receives a JSON array of paths. */
EXT_LEDGER_API int ext_ledger_traverse(ext_ledger *store, const ext_ledger_context *ctx, const char *ns, const char *start, int64_t max_depth, int64_t max_vertices, char **out, size_t *out_len);
EXT_LEDGER_API int ext_ledger_snapshot(ext_ledger *store, const ext_ledger_context *ctx, const char *ns, char **out, size_t *out_len);
/* ns may be "" for every namespace; *out receives a JSON array of changes. */
EXT_LEDGER_API int ext_ledger_changes(ext_ledger *store, const ext_ledger_context *ctx, const char *ns, uint64_t after, int64_t limit, char **out, size_t *out_len);

/* A watch replays the retained changes after the cursor and then delivers later
 * commits in cursor order. It ends when its buffer fills, the store closes or
 * its cursor falls below the history floor; the consumer resumes from the last
 * cursor it processed. */
EXT_LEDGER_API int ext_ledger_watch_open(ext_ledger *store, const ext_ledger_context *ctx, const char *ns, uint64_t after, int64_t buffer, ext_ledger_watch **out);
/* Waits up to timeout_ms (negative: without bound) for the next change.
 * Returns OK with the change, TIMEOUT when none came in time, and UNAVAILABLE
 * once the watch has ended and every buffered change was taken. */
EXT_LEDGER_API int ext_ledger_watch_next(ext_ledger_watch *watch, int64_t timeout_ms, char **change, size_t *change_len);
EXT_LEDGER_API void ext_ledger_watch_close(ext_ledger_watch *watch);

/* The canonical JSON and the fingerprint (lowercase hex SHA-256) of a transaction. */
EXT_LEDGER_API int ext_ledger_canonical_transaction(const char *transaction, size_t len, char **canonical, size_t *canonical_len, char fingerprint[65]);

/* ---- the ext.system recorder ----
 * The recorder projects observations of a machine (shell sessions, web
 * origins, host inventory, processes, adapter inventory) onto the ext.system
 * vocabulary in a store, and reads them back. Its contract is
 * res/graph/testdata/recorder/v1. */
typedef struct ext_ledger_recorder ext_ledger_recorder;

/* Registers the vocabulary on store and returns a recorder over it. environment
 * is the JSON object of the recorder fixtures (now, hostname, os,
 * architecture, parent_pid, working_directory, variables, paths,
 * host_environment): what the recorder would otherwise read from the machine.
 * The store must outlive the recorder. */
EXT_LEDGER_API int ext_ledger_recorder_open(ext_ledger *store, const ext_ledger_context *ctx, const char *environment, size_t len,
                                            ext_ledger_recorder **out);
/* A recorder of this machine: its environment is read from the OS, the clock
 * and the filesystem are read when each observation is made. */
EXT_LEDGER_API int ext_ledger_recorder_open_machine(ext_ledger *store, const ext_ledger_context *ctx, ext_ledger_recorder **out);
EXT_LEDGER_API void ext_ledger_recorder_close(ext_ledger_recorder *recorder);

/* Runs one recorder operation: observe_shell, observe_web, observe_host,
 * observe_processes or observe_inventory, which return nothing, or
 * resolve_inventory and read_host, which return JSON in *out. input is the
 * operation's JSON object (NULL or empty for none). An observation the recorder
 * refuses is EXT_LEDGER_INVALID; an operation this build does not have is
 * EXT_LEDGER_UNSUPPORTED. */
EXT_LEDGER_API int ext_ledger_record(ext_ledger_recorder *recorder, const ext_ledger_context *ctx, const char *op, const char *input, size_t len,
                                     char **out, size_t *out_len);

/* ---- the collectors ----
 * What the recorder reads from the machine through each kernel's own
 * interface, in the JSON forms of res/system's types. what is "processes"
 * (process identities, as res/system's DiscoverProcesses reports them),
 * "shells", "filesystems", "webviews", "resources", "desktop", "surfaces",
 * "accessibility" and "permissions" (as DiscoverShells, DiscoverFilesystems,
 * DiscoverWebviews, DiscoverResources, DiscoverDesktop, DiscoverSurfaces,
 * DiscoverAccessibility and DiscoverPermissions), or
 * "host" (the host inventory: every host kind this build collects here). A
 * collection this build or OS does not have is EXT_LEDGER_UNSUPPORTED. Scan
 * takes "processes" or "host". */
EXT_LEDGER_API int ext_ledger_collect(const ext_ledger_context *ctx, const char *what, char **out, size_t *out_len);
/* Collects what and records it through the recorder's observation of it. */
EXT_LEDGER_API int ext_ledger_scan(ext_ledger_recorder *recorder, const ext_ledger_context *ctx, const char *what);
/* The recorder environment of this machine, the JSON ext_ledger_recorder_open takes. */
EXT_LEDGER_API int ext_ledger_machine_environment(char **out, size_t *out_len);

/* The ext.system schema the recorder registers, as canonical JSON. */
EXT_LEDGER_API const char *ext_ledger_recorder_vocabulary(void);

EXT_LEDGER_API void ext_ledger_free(void *p);

#ifdef __cplusplus
}
#endif

#endif
