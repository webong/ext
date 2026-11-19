/* A small JSON reader and canonical writer, with no dependency beyond the C
 * standard library. Strings carry their length, so they may hold NUL. Parsing
 * follows the decoder the contract was written against: invalid UTF-8 and lone
 * surrogates become U+FFFD, a repeated object key keeps its last value, and
 * numbers are IEEE doubles. Writing is canonical JSON (README.md of the store
 * contract): keys sorted by UTF-16 code units, no whitespace, numbers in their
 * shortest round-trip form printed as ECMAScript prints them. */
#ifndef EXT_LEDGER_JSON_H
#define EXT_LEDGER_JSON_H

#include <stddef.h>
#include <stdint.h>

typedef enum { LJ_NULL, LJ_BOOL, LJ_NUMBER, LJ_STRING, LJ_ARRAY, LJ_OBJECT } lj_type;

typedef struct lj_value lj_value;

typedef struct {
  char *ptr; /* NUL-terminated copy */
  size_t len;
} lj_str;

typedef struct {
  lj_str key;
  lj_value *value;
} lj_member;

struct lj_value {
  lj_type type;
  union {
    int boolean;
    double number;
    lj_str string;
    struct {
      lj_value **items;
      size_t len, cap;
    } array;
    struct {
      lj_member *members;
      size_t len, cap;
    } object;
  } u;
};

/* Parsing. On failure returns NULL; *oom is set when memory ran out (it may be NULL). */
lj_value *lj_parse(const char *text, size_t len, int *oom);
void lj_free(lj_value *v);
lj_value *lj_clone(const lj_value *v);

/* Construction; every function returns NULL when memory runs out. */
lj_value *lj_new(lj_type type);
lj_value *lj_new_string(const char *s, size_t len);
lj_value *lj_new_number(double n);
int lj_array_push(lj_value *array, lj_value *item);              /* takes item; 0 on success */
int lj_object_set(lj_value *object, const char *key, size_t klen, lj_value *value); /* takes value, replaces */

/* Access. */
lj_value *lj_get(const lj_value *object, const char *key); /* NULL when absent or not an object */
int lj_equal(const lj_value *a, const lj_value *b);

/* A growable output buffer. */
typedef struct {
  char *ptr;
  size_t len, cap;
  int oom;
} lj_buf;

void lj_buf_put(lj_buf *b, const char *s, size_t n);
void lj_buf_puts(lj_buf *b, const char *s);
void lj_buf_putc(lj_buf *b, char c);
void lj_buf_free(lj_buf *b);
char *lj_buf_take(lj_buf *b, size_t *len); /* NUL-terminated; NULL on oom */

/* Canonical writing. */
void lj_write(lj_buf *b, const lj_value *v);
void lj_write_string(lj_buf *b, const char *s, size_t len);
void lj_write_number(lj_buf *b, double n);
void lj_write_uint(lj_buf *b, uint64_t n);

/* Keys in canonical order: compares by UTF-16 code units. */
int lj_key_compare(const char *a, size_t alen, const char *b, size_t blen);

#endif
