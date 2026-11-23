#ifndef EXT_LEDGER_INTERNAL_H
#define EXT_LEDGER_INTERNAL_H

#include "ext_ledger.h"
#include "json.h"

#include <stddef.h>
#include <stdint.h>

/* ---- hashing ---- */
void ll_sha256_hex(const void *data, size_t len, char out[65]);
uint32_t ll_crc32(const void *data, size_t len);

/* ---- strings ---- */
typedef struct {
  char *p; /* NUL-terminated, may be NULL when empty */
  size_t n;
} ll_s;

int ll_s_set(ll_s *s, const char *p, size_t n);
void ll_s_free(ll_s *s);
int ll_s_eq(const ll_s *a, const ll_s *b);
int ll_s_eqz(const ll_s *a, const char *z);
int ll_s_cmp(const ll_s *a, const ll_s *b); /* byte order, as Go compares strings */
#define LL_S(s) ((s).p ? (s).p : "")

/* ---- time ---- */
typedef struct {
  int set;      /* 0: the zero time, omitted from the wire form */
  int64_t sec;  /* Unix seconds of the instant */
  int32_t nsec; /* 0..999999999 */
  int32_t off;  /* the offset it was written with, in seconds */
} ll_time;

int ll_time_parse(const char *s, size_t n, ll_time *out); /* RFC 3339; 0 on success */
void ll_time_write(lj_buf *b, const ll_time *t);           /* RFC 3339 with nanoseconds, as Go prints it */
ll_time ll_time_now(void);
ll_time ll_time_utc(ll_time t);
int ll_time_is_zero(const ll_time *t); /* the instant 0001-01-01T00:00:00Z, or unset */

/* ---- records ----
 * Records are immutable once built and reference counted, so the state, the
 * retained history and results share them. */
typedef struct {
  ll_s source, operation;
  ll_time at;
} ll_prov;

typedef struct ll_record {
  int refs;
  int is_edge;
  ll_s ns, id, kind; /* kind is the edge type for an edge */
  ll_s from, to;
  uint64_t revision;
  ll_time created, updated;
  lj_value *labels; /* object of strings, or NULL when empty */
  lj_value *attrs;  /* object, or NULL when empty */
  ll_prov prov;
} ll_record;

ll_record *ll_record_new(int is_edge);
ll_record *ll_record_ref(ll_record *r);
void ll_record_unref(ll_record *r);
ll_record *ll_record_copy(const ll_record *r);
void ll_record_write(lj_buf *b, const ll_record *r);
int ll_prov_is_zero(const ll_prov *p);
void ll_prov_write(lj_buf *b, const ll_prov *p);
/* Decodes the wire form of a vertex or edge. */
int ll_record_decode(const lj_value *v, int is_edge, ll_record **out);

typedef struct {
  ll_s ns, id;
} ll_ref;

typedef struct {
  ll_s type, from, to;
} ll_rel;

typedef struct {
  ll_s ns, version;
  ll_s *kinds;
  size_t nkinds;
  ll_rel *rels;
  size_t nrels;
} ll_schema;

int ll_schema_decode(const lj_value *v, ll_schema *out);
void ll_schema_free(ll_schema *s);
int ll_schema_copy(const ll_schema *s, ll_schema *out);
void ll_schema_write(lj_buf *b, const ll_schema *s);
int ll_schema_normalize(ll_schema *s); /* sorts and deduplicates, in place */
int ll_schema_validate(const ll_schema *s);
int ll_schema_same(const ll_schema *a, const ll_schema *b); /* both normalised */

typedef struct {
  ll_s ns, key;
  int has_expected;
  uint64_t expected;
  ll_record **vertices;
  size_t nvertices;
  ll_record **edges;
  size_t nedges;
  ll_ref *delete_vertices;
  size_t ndelete_vertices;
  ll_ref *delete_edges;
  size_t ndelete_edges;
  int delete_incident;
} ll_tx;

int ll_tx_decode(const lj_value *v, ll_tx *out);
void ll_tx_free(ll_tx *t);
void ll_tx_write(lj_buf *b, const ll_tx *t);

typedef struct {
  int refs;
  uint64_t cursor, revision;
  ll_s ns;
  ll_time committed;
  ll_record **vertices;
  size_t nvertices, capv;
  ll_record **edges;
  size_t nedges, cape;
  ll_s *deleted_vertices;
  size_t ndeleted_vertices, capdv;
  ll_s *deleted_edges;
  size_t ndeleted_edges, capde;
} ll_change;

ll_change *ll_change_new(void);
ll_change *ll_change_ref(ll_change *c);
void ll_change_unref(ll_change *c);
void ll_change_write(lj_buf *b, const ll_change *c);
int ll_change_decode(const lj_value *v, ll_change **out);

/* ---- rules (rules.json) ---- */
int ll_check_name(const char *s, size_t n);
int ll_qualified(const ll_s *ns, const ll_s *name);
int ll_check_tx(const ll_tx *t, const ext_ledger_store_options *o);
ext_ledger_store_options ll_options_defaults(const ext_ledger_store_options *o);
int ll_check_limit(int64_t limit, const ext_ledger_store_options *o);

/* ---- a string-keyed hash map ---- */
typedef struct {
  ll_s key;
  void *value;
  int used; /* 0 empty, 1 used, 2 deleted */
} ll_slot;

typedef struct {
  ll_slot *slots;
  size_t cap, len, deleted;
} ll_map;

void *ll_map_get(const ll_map *m, const char *k, size_t n);
int ll_map_put(ll_map *m, const char *k, size_t n, void *value); /* replaces; 0 on success */
void *ll_map_del(ll_map *m, const char *k, size_t n);             /* returns the removed value */
void ll_map_free(ll_map *m);
#define LL_MAP_EACH(m, slot) for (ll_slot *slot = (m)->slots; slot && slot < (m)->slots + (m)->cap; slot++) if (slot->used == 1)

/* ---- the graph state every backend keeps in memory ---- */
typedef struct {
  ll_schema schema;
  uint64_t revision;
  ll_map vertices; /* id -> ll_record* */
  ll_map edges;    /* id -> ll_record* */
} ll_namespace;

typedef struct {
  ll_change *change;
  size_t size;
  ll_s key, fingerprint; /* the idempotency key, empty when none */
} ll_retained;

typedef struct {
  char fingerprint[65];
  uint64_t cursor, revision;
  ll_time committed;
} ll_idem;

typedef struct {
  uint64_t cursor, floor;
  ll_map namespaces; /* name -> ll_namespace* */
  ll_retained *changes; /* changes[first..nchanges) are retained, in cursor order */
  size_t first, nchanges, capchanges;
  size_t change_bytes;
  ll_map idempotency; /* ns \0 key -> ll_idem* */
} ll_state;

void ll_state_init(ll_state *s);
void ll_state_free(ll_state *s);
ll_namespace *ll_state_ns(ll_state *s, const char *ns, size_t n);
/* Sets a namespace's schema as a replayed or accepted registration does. */
int ll_state_set_schema(ll_state *s, const ll_schema *schema);
/* The check Register makes against the stored schema: OK to store, EXT_LEDGER_EXISTS on a mismatch.
 * *changed is set when storing it changes anything. */
int ll_state_check_register(ll_state *s, const ll_schema *schema, int *changed);
int ll_state_prepare(ll_state *s, const ext_ledger_context *ctx, const ll_tx *t, ll_time now, ll_change **out);
/* Retains a change whose cursor is set, with its idempotency key. With graph
 * set it also applies the change to the records and advances the cursor; a
 * replayed commit at or below a checkpoint's base is history only. */
int ll_state_apply(ll_state *s, ll_change *c, const ll_s *key, const ll_s *fingerprint, int graph);
/* Loads a record of a checkpoint into its (registered) namespace. */
int ll_state_put(ll_state *s, ll_record *r);
/* Loads an idempotency record of a retained commit. */
int ll_state_put_idem(ll_state *s, const ll_s *ns, const ll_s *key, const ll_idem *idem);
/* The floor compaction would move to under the bounds; equal to s->floor when none. */
uint64_t ll_state_compaction(const ll_state *s, const ext_ledger_store_options *o);
void ll_state_set_floor(ll_state *s, uint64_t floor);
const ll_idem *ll_state_idempotent(const ll_state *s, const ll_s *ns, const ll_s *key);
size_t ll_state_retained(const ll_state *s); /* the number of retained changes */
const ll_retained *ll_state_retained_at(const ll_state *s, size_t i);
/* Records of a namespace whose from (or to) is id, sorted by ID; *out is malloc'd. */
int ll_state_incident(ll_namespace *ns, const ll_s *id, int outgoing_only, ll_record ***out, size_t *n);
int ll_record_id_cmp(const void *a, const void *b); /* qsort over ll_record* by ID */

/* ---- flags ----
 * An int one thread sets and another polls, such as a context's canceled. */
#if defined(_MSC_VER) && !defined(__clang__)
#include <intrin.h>
static __inline int ll_flag_load(const volatile int *p) { return (int)_InterlockedOr((volatile long *)p, 0); }
static __inline void ll_flag_store(volatile int *p, int v) { _InterlockedExchange((volatile long *)p, v); }
#else
static inline int ll_flag_load(const volatile int *p) { return __atomic_load_n(p, __ATOMIC_ACQUIRE); }
static inline void ll_flag_store(volatile int *p, int v) { __atomic_store_n(p, v, __ATOMIC_RELEASE); }
#endif

/* ---- contexts ---- */
int ll_ctx_check(const ext_ledger_context *ctx);
int64_t ll_now_unix_ms(void);

/* ---- platform ---- */
typedef struct ll_mutex ll_mutex;
typedef struct ll_cond ll_cond;
ll_mutex *ll_mutex_new(void);
void ll_mutex_free(ll_mutex *m);
void ll_mutex_lock(ll_mutex *m);
void ll_mutex_unlock(ll_mutex *m);
ll_cond *ll_cond_new(void);
void ll_cond_free(ll_cond *c);
void ll_cond_wait(ll_cond *c, ll_mutex *m, int64_t timeout_ms); /* negative: no bound */
void ll_cond_broadcast(ll_cond *c);
void ll_sleep_ms(int64_t ms);

/* Files. Every function answers 0 or an EXT_LEDGER_ code (EXT_LEDGER_IO for an OS failure). */
typedef struct ll_file ll_file;
typedef struct {
  uint64_t a, b; /* device and inode, or volume and file index */
} ll_fid;
enum { LL_READ = 1, LL_WRITE = 2, LL_CREATE = 4, LL_TRUNC = 8 };
int ll_file_open(const char *path, int flags, ll_file **out); /* EXT_LEDGER_NOT_FOUND when absent */
int ll_file_read_at(ll_file *f, uint64_t off, void *buf, size_t n, size_t *got);
int ll_file_write_at(ll_file *f, uint64_t off, const void *buf, size_t n);
int ll_file_size(ll_file *f, uint64_t *size);
int ll_file_truncate(ll_file *f, uint64_t size);
int ll_file_sync(ll_file *f);
int ll_file_identity(ll_file *f, ll_fid *id);
int ll_file_try_lock(ll_file *f); /* 0 when held, EXT_LEDGER_BUSY when another holds it */
void ll_file_unlock(ll_file *f);
void ll_file_close(ll_file *f);
int ll_path_identity(const char *path, ll_fid *id); /* EXT_LEDGER_NOT_FOUND when absent */
int ll_path_kind(const char *path);                  /* 0 absent, 1 directory, 2 something else, <0 failure */
int ll_mkdir(const char *path);                      /* creates it (mode 0700) when absent */
int ll_list_dir(const char *dir, int (*each)(void *arg, const char *name), void *arg);
int ll_rename(const char *from, const char *to); /* replaces to */
int ll_remove(const char *path);                 /* absent is not an error */
int ll_sync_dir(const char *dir);

/* ---- backends ----
 * Every backend keeps the whole state in memory and persists registrations and
 * commits. A durable backend also catches up with what other handles wrote. */
typedef struct ll_backend ll_backend;
typedef struct {
  /* Brings state up to date with the storage. */
  int (*refresh)(ll_backend *b, ll_state *state);
  /* Takes the writer lock and refreshes. */
  int (*begin_write)(ll_backend *b, const ext_ledger_context *ctx, ll_state *state);
  void (*end_write)(ll_backend *b);
  int (*write_schema)(ll_backend *b, ll_state *state, const ll_schema *schema);
  /* Persists a commit, and the new floor when it moved. */
  int (*write_commit)(ll_backend *b, ll_state *state, const ll_retained *commit, uint64_t floor, int floor_moved);
  /* Runs after a commit with the floor applied, still under the writer lock: may rewrite the storage. */
  void (*maintain)(ll_backend *b, ll_state *state);
  void (*close)(ll_backend *b);
} ll_backend_ops;

struct ll_backend {
  const ll_backend_ops *ops;
};

int ll_dir_open(const ext_ledger_context *ctx, const char *dir, int read_only, ll_state *state, ll_backend **out);
int ll_sqlite_open(const ext_ledger_context *ctx, const char *path, ll_state *state, ll_backend **out);
/* The SQLite backend's CREATE statements, NULL-terminated; NULL in a build without it. */
const char *const *ll_sqlite_layout(void);

#endif
