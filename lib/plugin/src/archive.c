#define _POSIX_C_SOURCE 200809L
#include "archive.h"
#include "services.h"
#include "sha256.h"
#include <stdlib.h>
#include <string.h>

/* Package archives: zip, gzip-compressed tar and plain tar. Each format is one
 * reader that walks the entries of an archive and hands every one to the
 * extraction, which owns the bounds, the path checks and the refusal of links
 * and special files, so those are written once. A new format is one more
 * reader and one more line in detect().
 *
 * Inflate is miniz's tinfl decoder (vendor/miniz), compiled into this unit and
 * nowhere else. Everything around it, the containers, CRC-32 and tar, is here. */

/* Every function the decoder defines takes a private name, so none of them
 * meets a consumer's own miniz or zlib at link time. */
#define tinfl_decompress ext_host_private_tinfl_decompress
#define tinfl_decompress_mem_to_heap ext_host_private_tinfl_decompress_mem_to_heap
#define tinfl_decompress_mem_to_mem ext_host_private_tinfl_decompress_mem_to_mem
#define tinfl_decompress_mem_to_callback ext_host_private_tinfl_decompress_mem_to_callback
#define MINIZ_NO_MALLOC
#define MINIZ_NO_STDIO
#define MINIZ_NO_TIME
#if defined(__GNUC__) || defined(__clang__)
#pragma GCC diagnostic push
#pragma GCC diagnostic ignored "-Wunused-function"
#pragma GCC diagnostic ignored "-Wunused-parameter"
#pragma GCC diagnostic ignored "-Wsign-compare"
#pragma GCC diagnostic ignored "-Wimplicit-fallthrough"
#pragma GCC diagnostic ignored "-Wunused-value"
#endif
#include "miniz/miniz_tinfl.c"
#if defined(__GNUC__) || defined(__clang__)
#pragma GCC diagnostic pop
#endif

#define BLOCK 512u
#define PAX_MAX (1u << 20) /* one extended header, as Go's archive/tar allows */

static uint16_t le16(const uint8_t *p) { return (uint16_t)(p[0] | p[1] << 8); }
static uint32_t le32(const uint8_t *p) {
  return (uint32_t)p[0] | (uint32_t)p[1] << 8 | (uint32_t)p[2] << 16 |
         (uint32_t)p[3] << 24;
}

/* CRC-32 as zip uses it, four bits at a time. */
static uint32_t crc32_update(uint32_t crc, const uint8_t *data, size_t len) {
  static const uint32_t table[16] = {
      0x00000000u, 0x1db71064u, 0x3b6e20c8u, 0x26d930acu, 0x76dc4190u, 0x6b6b51f4u,
      0x4db26158u, 0x5005713cu, 0xedb88320u, 0xf00f9344u, 0xd6d6a3e8u, 0xcb61b38cu,
      0x9b64c2b0u, 0x86d3d2d4u, 0xa00ae278u, 0xbdbdf21cu};
  crc = ~crc;
  while (len--) {
    crc ^= *data++;
    crc = table[crc & 15] ^ (crc >> 4);
    crc = table[crc & 15] ^ (crc >> 4);
  }
  return ~crc;
}

int ext_archive_digest_text(const char *text, char out[65]) {
  if (!text || strlen(text) != 64)
    return 0;
  for (size_t i = 0; i < 64; i++) {
    char c = text[i];
    if (c >= 'A' && c <= 'F')
      c = (char)(c + 'a' - 'A');
    if (!((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')))
      return 0;
    out[i] = c;
  }
  out[64] = 0;
  return 1;
}

/* ---- streams: bytes in order, from a part of the file or through inflate ---- */

typedef struct stream stream;
struct stream {
  /* Fills data unless the stream ends first. A stream that cannot be read as
   * what it claims to be is EXT_PLUGIN_INVALID. */
  ext_plugin_status (*read)(stream *s, uint8_t *data, size_t len, size_t *got);
};
static ext_plugin_status read_full(stream *s, uint8_t *data, size_t len) {
  size_t got = 0;
  ext_plugin_status status = s->read(s, data, len, &got);
  return status ? status : got == len ? EXT_PLUGIN_OK : EXT_PLUGIN_INVALID;
}

typedef struct {
  stream base;
  ext_fs_in *in;
  uint64_t at, end;
} span;
static ext_plugin_status span_read(stream *s, uint8_t *data, size_t len, size_t *got) {
  span *p = (span *)s;
  uint64_t left = p->end - p->at;
  if (len > left)
    len = (size_t)left;
  ext_plugin_status status = ext_fs_in_read(p->in, p->at, data, len, got);
  if (!status)
    p->at += *got;
  return status;
}
static void span_init(span *p, ext_fs_in *in, uint64_t from, uint64_t end) {
  p->base.read = span_read;
  p->in = in;
  p->at = from;
  p->end = end;
}

typedef struct {
  stream base;
  stream *source;
  tinfl_decompressor state;
  uint8_t input[32768];
  size_t input_at, input_len;
  int input_ended, done;
  uint8_t window[TINFL_LZ_DICT_SIZE];
  size_t window_at;            /* where the decoder writes next */
  size_t pending_at, pending;  /* decoded bytes not handed out yet */
} inflater;
static ext_plugin_status inflate_read(stream *s, uint8_t *data, size_t len,
                               size_t *got) {
  inflater *z = (inflater *)s;
  size_t total = 0;
  while (total < len) {
    if (z->pending) {
      size_t n = z->pending < len - total ? z->pending : len - total;
      memcpy(data + total, z->window + z->pending_at, n);
      z->pending_at += n;
      z->pending -= n;
      total += n;
      continue;
    }
    if (z->done)
      break;
    if (z->input_at == z->input_len && !z->input_ended) {
      size_t n = 0;
      ext_plugin_status status = z->source->read(z->source, z->input, sizeof(z->input), &n);
      if (status)
        return status;
      z->input_at = 0;
      z->input_len = n;
      if (n < sizeof(z->input))
        z->input_ended = 1;
    }
    size_t in = z->input_len - z->input_at;
    size_t out = TINFL_LZ_DICT_SIZE - z->window_at;
    tinfl_status status = tinfl_decompress(
        &z->state, z->input + z->input_at, &in, z->window,
        z->window + z->window_at, &out,
        z->input_ended ? 0 : TINFL_FLAG_HAS_MORE_INPUT);
    z->input_at += in;
    z->pending_at = z->window_at;
    z->pending = out;
    z->window_at = (z->window_at + out) & (TINFL_LZ_DICT_SIZE - 1);
    if (status < TINFL_STATUS_DONE)
      return EXT_PLUGIN_INVALID;
    if (status == TINFL_STATUS_DONE)
      z->done = 1;
    else if (status == TINFL_STATUS_NEEDS_MORE_INPUT && z->input_ended && !in &&
             !out)
      return EXT_PLUGIN_INVALID; /* the deflate stream is cut short */
  }
  *got = total;
  return EXT_PLUGIN_OK;
}
static inflater *inflate_open(stream *source) {
  inflater *z = malloc(sizeof(*z));
  if (!z)
    return NULL;
  z->base.read = inflate_read;
  z->source = source;
  tinfl_init(&z->state);
  z->input_at = z->input_len = 0;
  z->input_ended = z->done = 0;
  z->window_at = z->pending_at = z->pending = 0;
  return z;
}

/* ---- the extraction: what every format's entries go through ---- */

typedef struct {
  char *key; /* the path in lower case */
  int dir;
} seen_entry;
typedef struct {
  ext_fs *fs;
  const char *dest;
  ext_archive_limits limits;
  uint64_t entries, bytes;
  seen_entry *seen; /* open addressing, capacity a power of two */
  size_t seen_cap, seen_len;
} extraction;

static uint64_t hash_text(const char *s) {
  uint64_t h = 1469598103934665603ull;
  while (*s)
    h = (h ^ (uint8_t)*s++) * 1099511628211ull;
  return h;
}
static seen_entry *seen_find(extraction *x, const char *key) {
  if (!x->seen_cap)
    return NULL;
  for (size_t i = (size_t)hash_text(key) & (x->seen_cap - 1);;
       i = (i + 1) & (x->seen_cap - 1)) {
    seen_entry *e = &x->seen[i];
    if (!e->key)
      return NULL;
    if (!strcmp(e->key, key))
      return e;
  }
}
/* Takes ownership of key. */
static ext_plugin_status seen_add(extraction *x, char *key, int dir) {
  if ((x->seen_len + 1) * 2 > x->seen_cap) {
    size_t cap = x->seen_cap ? x->seen_cap * 2 : 64;
    seen_entry *grown = calloc(cap, sizeof(*grown));
    if (!grown) {
      free(key);
      return EXT_PLUGIN_NOMEM;
    }
    for (size_t i = 0; i < x->seen_cap; i++) {
      if (!x->seen[i].key)
        continue;
      size_t j = (size_t)hash_text(x->seen[i].key) & (cap - 1);
      while (grown[j].key)
        j = (j + 1) & (cap - 1);
      grown[j] = x->seen[i];
    }
    free(x->seen);
    x->seen = grown;
    x->seen_cap = cap;
  }
  size_t i = (size_t)hash_text(key) & (x->seen_cap - 1);
  while (x->seen[i].key)
    i = (i + 1) & (x->seen_cap - 1);
  x->seen[i].key = key;
  x->seen[i].dir = dir;
  x->seen_len++;
  return EXT_PLUGIN_OK;
}
static void seen_free(extraction *x) {
  for (size_t i = 0; i < x->seen_cap; i++)
    free(x->seen[i].key);
  free(x->seen);
}

/* A slash-separated relative path an entry may use on every platform: no
 * empty, "." or ".." element, no backslash, colon or control character, no
 * element ending in a dot or a space, no Windows device name. Its UTF-8 is
 * checked by the caller. */
static int safe_path(const char *name) {
  size_t n = strlen(name);
  if (!n)
    return 0;
  for (size_t i = 0; i < n; i++) {
    uint8_t c = (uint8_t)name[i];
    if (c < 0x20 || c == 0x7f || c == '\\' || c == ':')
      return 0;
  }
  for (size_t start = 0; start <= n;) {
    const char *slash = memchr(name + start, '/', n - start);
    size_t len = slash ? (size_t)(slash - (name + start)) : n - start;
    const char *part = name + start;
    if (!len || len > 255 || (len == 1 && part[0] == '.') ||
        (len == 2 && part[0] == '.' && part[1] == '.') ||
        part[len - 1] == '.' || part[len - 1] == ' ')
      return 0;
    char stem[5] = {0};
    size_t m = 0;
    while (m < len && part[m] != '.' && m < 5)
      m++;
    if (m <= 4 && (m == len || part[m] == '.')) {
      for (size_t i = 0; i < m; i++)
        stem[i] = part[i] >= 'a' && part[i] <= 'z' ? (char)(part[i] - 32) : part[i];
      if (!strcmp(stem, "CON") || !strcmp(stem, "PRN") || !strcmp(stem, "AUX") ||
          !strcmp(stem, "NUL") ||
          (m == 4 && (!memcmp(stem, "COM", 3) || !memcmp(stem, "LPT", 3)) &&
           stem[3] >= '1' && stem[3] <= '9'))
        return 0;
    }
    if (!slash)
      break;
    start += len + 1;
  }
  return 1;
}

static char *join2(const char *a, const char *b) {
  size_t n = strlen(a), m = strlen(b);
  char *out = malloc(n + m + 2);
  if (!out)
    return NULL;
  memcpy(out, a, n);
  out[n] = '/';
  memcpy(out + n + 1, b, m + 1);
  return out;
}

/* Validates and records one entry name. *target is its path below fs, or NULL
 * for an entry that needs nothing: the archive's own top directory, or a
 * directory already made. */
static ext_plugin_status claim(extraction *x, const char *raw, int dir, char **target) {
  *target = NULL;
  size_t n = strlen(raw);
  if (n && raw[n - 1] == '/')
    n--;
  while (n >= 2 && raw[0] == '.' && raw[1] == '/') {
    raw += 2;
    n -= 2;
  }
  if (dir && (!n || (n == 1 && raw[0] == '.')))
    return EXT_PLUGIN_OK;
  char *name = malloc(n + 1);
  if (!name)
    return EXT_PLUGIN_NOMEM;
  memcpy(name, raw, n);
  name[n] = 0;
  char *key = safe_path(name) ? ext_path_fold_key(name) : NULL;
  ext_plugin_status s = key ? EXT_PLUGIN_OK : EXT_PLUGIN_INVALID;
  if (!s && ++x->entries > x->limits.max_tree_files)
    s = EXT_PLUGIN_INVALID;
  if (!s) {
    seen_entry *old = seen_find(x, key);
    if (old) {
      /* A directory may be named again; nothing else may. */
      s = dir && old->dir ? EXT_PLUGIN_OK : EXT_PLUGIN_INVALID;
      free(key);
      free(name);
      return s;
    }
  }
  if (s) {
    free(key);
    free(name);
    return s;
  }
  /* The directories above an entry exist once it does, listed or not: a later
   * file may not take the place of one, and no entry sits under a file. */
  size_t key_len = strlen(key);
  char *parent = malloc(key_len + 1);
  if (!parent)
    s = EXT_PLUGIN_NOMEM;
  else
    memcpy(parent, key, key_len + 1);
  if (!s)
    s = seen_add(x, key, dir); /* key is the table's now */
  else
    free(key);
  while (!s) {
    char *cut = strrchr(parent, '/');
    if (!cut)
      break;
    *cut = 0;
    seen_entry *above = seen_find(x, parent);
    if (above) {
      if (!above->dir)
        s = EXT_PLUGIN_INVALID;
      continue;
    }
    char *copy = malloc(strlen(parent) + 1);
    if (!copy) {
      s = EXT_PLUGIN_NOMEM;
      break;
    }
    strcpy(copy, parent);
    s = seen_add(x, copy, 1);
  }
  free(parent);
  if (!s) {
    *target = join2(x->dest, name);
    if (!*target)
      s = EXT_PLUGIN_NOMEM;
  }
  free(name);
  return s;
}

static ext_plugin_status extract_directory(extraction *x, const char *raw) {
  char *target;
  ext_plugin_status s = claim(x, raw, 1, &target);
  if (!s && target)
    s = ext_fs_mkdirs(x->fs, target);
  free(target);
  return s;
}
/* Writes one file of exactly the declared size, which must fit the bounds.
 * *crc, when given, receives the CRC-32 of what was written. */
static ext_plugin_status extract_file(extraction *x, const char *raw, int executable,
                               uint64_t declared, stream *body, uint32_t *crc) {
  char *target;
  ext_plugin_status s = claim(x, raw, 0, &target);
  if (s)
    return s;
  uint64_t allowed = x->limits.max_tree_bytes - x->bytes;
  if (allowed > x->limits.max_artifact_bytes)
    allowed = x->limits.max_artifact_bytes;
  if (declared > allowed) {
    free(target);
    return EXT_PLUGIN_INVALID;
  }
  ext_fs_out *out = NULL;
  s = ext_fs_create(x->fs, target, executable, &out);
  free(target);
  if (s)
    return s;
  uint8_t *buffer = malloc(65536);
  if (!buffer)
    s = EXT_PLUGIN_NOMEM;
  uint64_t written = 0;
  uint32_t sum = 0;
  while (!s && written < declared) {
    size_t want = declared - written < 65536 ? (size_t)(declared - written) : 65536;
    size_t got = 0;
    s = body->read(body, buffer, want, &got);
    if (!s && !got)
      s = EXT_PLUGIN_INVALID; /* shorter than declared */
    if (!s) {
      sum = crc32_update(sum, buffer, got);
      s = ext_fs_out_write(out, buffer, got);
      written += got;
    }
  }
  if (!s) {
    /* ... and no longer. */
    size_t got = 0;
    s = body->read(body, buffer, 1, &got);
    if (!s && got)
      s = EXT_PLUGIN_INVALID;
  }
  free(buffer);
  ext_plugin_status closed = ext_fs_out_close(out);
  if (!s)
    s = closed;
  if (!s) {
    x->bytes += written;
    if (crc)
      *crc = sum;
  }
  return s;
}

/* ---- tar ---- */

static int zero_block(const uint8_t *b) {
  for (size_t i = 0; i < BLOCK; i++)
    if (b[i])
      return 0;
  return 1;
}
/* A numeric header field: octal text, or the GNU base-256 form. */
static int tar_number(const uint8_t *field, size_t len, uint64_t *out) {
  uint64_t v = 0;
  if (field[0] & 0x80) {
    if (field[0] != 0x80)
      return 0; /* negative, or wider than any size here */
    for (size_t i = 1; i < len; i++) {
      if (v >> 56)
        return 0;
      v = v << 8 | field[i];
    }
    *out = v;
    return 1;
  }
  size_t i = 0;
  while (i < len && field[i] == ' ')
    i++;
  for (; i < len && field[i] && field[i] != ' '; i++) {
    if (field[i] < '0' || field[i] > '7' || v >> 61)
      return 0;
    v = v << 3 | (uint64_t)(field[i] - '0');
  }
  *out = v;
  return 1;
}
static int tar_checksum_ok(const uint8_t *b) {
  uint64_t want;
  if (!tar_number(b + 148, 8, &want))
    return 0;
  uint64_t plain = 0;
  int64_t with_sign = 0;
  for (size_t i = 0; i < BLOCK; i++) {
    uint8_t c = i >= 148 && i < 156 ? ' ' : b[i];
    plain += c;
    with_sign += (int8_t)c;
  }
  return want == plain || (int64_t)want == with_sign;
}
static char *field_text(const uint8_t *field, size_t len) {
  size_t n = 0;
  while (n < len && field[n])
    n++;
  char *out = malloc(n + 1);
  if (out) {
    memcpy(out, field, n);
    out[n] = 0;
  }
  return out;
}
/* Reads the data of a header that only describes the next one. */
static ext_plugin_status tar_special(stream *in, uint64_t size, uint8_t **out) {
  *out = NULL;
  if (size > PAX_MAX)
    return EXT_PLUGIN_INVALID;
  size_t padded = (size_t)((size + BLOCK - 1) / BLOCK * BLOCK);
  uint8_t *data = malloc(padded + 1);
  if (!data)
    return EXT_PLUGIN_NOMEM;
  ext_plugin_status s = read_full(in, data, padded);
  if (s) {
    free(data);
    return s;
  }
  data[size] = 0;
  *out = data;
  return EXT_PLUGIN_OK;
}
/* The records of an extended header: "<length> <key>=<value>\n". Keeps the
 * path and the size; a sparse file is not supported. */
static ext_plugin_status pax_records(const uint8_t *data, size_t size, char **path,
                              int *has_size, uint64_t *file_size) {
  size_t at = 0;
  while (at < size) {
    size_t len = 0, digits = 0;
    while (at + digits < size && data[at + digits] >= '0' &&
           data[at + digits] <= '9' && digits < 9)
      len = len * 10 + (size_t)(data[at + digits++] - '0');
    if (!digits || at + digits >= size || data[at + digits] != ' ' ||
        len <= digits + 1 || len > size - at || data[at + len - 1] != '\n')
      return EXT_PLUGIN_INVALID;
    const uint8_t *record = data + at + digits + 1;
    size_t record_len = len - digits - 2;
    const uint8_t *eq = memchr(record, '=', record_len);
    if (!eq || eq == record)
      return EXT_PLUGIN_INVALID;
    size_t key_len = (size_t)(eq - record), value_len = record_len - key_len - 1;
    const uint8_t *value = eq + 1;
    if (key_len == 4 && !memcmp(record, "path", 4)) {
      if (!value_len || memchr(value, 0, value_len))
        return EXT_PLUGIN_INVALID;
      free(*path);
      *path = field_text(value, value_len);
      if (!*path)
        return EXT_PLUGIN_NOMEM;
    } else if (key_len == 4 && !memcmp(record, "size", 4)) {
      uint64_t v = 0;
      if (!value_len)
        return EXT_PLUGIN_INVALID;
      for (size_t i = 0; i < value_len; i++) {
        if (value[i] < '0' || value[i] > '9' || v > (UINT64_MAX - 9) / 10)
          return EXT_PLUGIN_INVALID;
        v = v * 10 + (uint64_t)(value[i] - '0');
      }
      *has_size = 1;
      *file_size = v;
    } else if (key_len >= 11 && !memcmp(record, "GNU.sparse.", 11))
      return EXT_PLUGIN_INVALID;
    at += len;
  }
  return EXT_PLUGIN_OK;
}
/* Hands the rest of one tar body to the extraction: exactly size bytes. */
typedef struct {
  stream base;
  stream *source;
  uint64_t left;
} bounded;
static ext_plugin_status bounded_read(stream *s, uint8_t *data, size_t len,
                               size_t *got) {
  bounded *b = (bounded *)s;
  if (len > b->left)
    len = (size_t)b->left;
  ext_plugin_status status = b->source->read(b->source, data, len, got);
  if (!status) {
    if (*got < len)
      return EXT_PLUGIN_INVALID; /* the archive ends inside a file */
    b->left -= *got;
  }
  return status;
}
static ext_plugin_status read_tar(stream *in, extraction *x) {
  uint8_t block[BLOCK];
  char *long_name = NULL, *pax_path = NULL;
  int pax_has_size = 0;
  uint64_t pax_size = 0;
  ext_plugin_status s = EXT_PLUGIN_OK;
  while (!s) {
    size_t got = 0;
    s = in->read(in, block, BLOCK, &got);
    if (s || !got)
      break; /* an archive may end without its two empty blocks */
    if (got != BLOCK) {
      s = EXT_PLUGIN_INVALID;
      break;
    }
    if (zero_block(block)) {
      s = in->read(in, block, BLOCK, &got);
      if (!s && got && (got != BLOCK || !zero_block(block)))
        s = EXT_PLUGIN_INVALID;
      break;
    }
    uint64_t size = 0, mode = 0;
    if (!tar_checksum_ok(block) || memcmp(block + 257, "ustar", 5) ||
        !tar_number(block + 124, 12, &size) || !tar_number(block + 100, 8, &mode)) {
      s = EXT_PLUGIN_INVALID;
      break;
    }
    uint8_t type = block[156];
    if (type == 'x' || type == 'g' || type == 'L' || type == 'K') {
      uint8_t *data = NULL;
      s = tar_special(in, size, &data);
      if (!s && type == 'x')
        s = pax_records(data, (size_t)size, &pax_path, &pax_has_size, &pax_size);
      else if (!s && type == 'g') {
        /* For the whole archive: checked, and nothing in it is used. */
        char *ignored = NULL;
        int has = 0;
        uint64_t value = 0;
        s = pax_records(data, (size_t)size, &ignored, &has, &value);
        free(ignored);
      } else if (!s && type == 'L') {
        free(long_name);
        long_name = field_text(data, (size_t)size);
        if (!long_name)
          s = EXT_PLUGIN_NOMEM;
      }
      free(data);
      continue;
    }
    char *name = NULL;
    if (pax_path) {
      name = pax_path;
      pax_path = NULL;
    } else if (long_name) {
      name = long_name;
      long_name = NULL;
    } else {
      char *leaf = field_text(block + 0, 100);
      /* The prefix belongs to the POSIX format; GNU uses those bytes otherwise. */
      char *prefix = !memcmp(block + 257, "ustar\0", 6) && block[345]
                         ? field_text(block + 345, 155)
                         : NULL;
      if (!leaf || (block[345] && !memcmp(block + 257, "ustar\0", 6) && !prefix))
        s = EXT_PLUGIN_NOMEM;
      else if (prefix) {
        name = join2(prefix, leaf);
        if (!name)
          s = EXT_PLUGIN_NOMEM;
        free(leaf);
      } else
        name = leaf;
      if (s)
        free(leaf);
      free(prefix);
    }
    free(long_name);
    free(pax_path);
    long_name = pax_path = NULL;
    if (pax_has_size)
      size = pax_size;
    pax_has_size = 0;
    if (s) {
      free(name);
      break;
    }
    size_t name_len = strlen(name);
    if (type == 0 && name_len && name[name_len - 1] == '/')
      type = '5'; /* the old way to mark a directory */
    if (type == '5')
      s = extract_directory(x, name);
    else if (type == '0' || type == 0) {
      bounded body = {{bounded_read}, in, size};
      s = extract_file(x, name, (mode & 0100) != 0, size, &body.base, NULL);
      uint64_t padding = (BLOCK - size % BLOCK) % BLOCK;
      if (!s && padding)
        s = read_full(in, block, (size_t)padding);
    } else
      s = EXT_PLUGIN_INVALID; /* a link, a device, a pipe, a sparse file */
    free(name);
  }
  free(long_name);
  free(pax_path);
  return s;
}

/* The gzip member header in front of the deflate stream (RFC 1952). */
static ext_plugin_status gzip_header(stream *in) {
  uint8_t h[10], b;
  ext_plugin_status s = read_full(in, h, sizeof(h));
  if (s)
    return s;
  if (h[0] != 0x1f || h[1] != 0x8b || h[2] != 8 || (h[3] & 0xe0))
    return EXT_PLUGIN_INVALID;
  if (h[3] & 4) { /* extra field */
    uint8_t n[2];
    s = read_full(in, n, 2);
    for (uint32_t left = le16(n); !s && left; left--)
      s = read_full(in, &b, 1);
  }
  for (int field = 8; !s && field <= 16; field <<= 1) /* name, comment */
    if (h[3] & field)
      do
        s = read_full(in, &b, 1);
      while (!s && b);
  if (!s && (h[3] & 2)) { /* header checksum */
    uint8_t n[2];
    s = read_full(in, n, 2);
  }
  return s;
}
static ext_plugin_status read_tar_gzip(ext_fs_in *in, uint64_t size, extraction *x) {
  span raw;
  span_init(&raw, in, 0, size);
  ext_plugin_status s = gzip_header(&raw.base);
  if (s)
    return EXT_PLUGIN_INVALID;
  inflater *z = inflate_open(&raw.base);
  if (!z)
    return EXT_PLUGIN_NOMEM;
  s = read_tar(&z->base, x);
  free(z);
  return s;
}
static ext_plugin_status read_tar_plain(ext_fs_in *in, uint64_t size, extraction *x) {
  span raw;
  span_init(&raw, in, 0, size);
  return read_tar(&raw.base, x);
}

/* ---- zip ---- */

#define ZIP_END 22u
#define ZIP_CENTRAL 46u
#define ZIP_LOCAL 30u

static ext_plugin_status read_zip(ext_fs_in *in, uint64_t size, extraction *x) {
  /* The end record is the last thing in the file, before a comment of at most
   * 65535 bytes. Archives in the zip64 form are not read. */
  size_t tail_len = size < ZIP_END + 65535u ? (size_t)size : ZIP_END + 65535u;
  if (tail_len < ZIP_END)
    return EXT_PLUGIN_INVALID;
  uint8_t *tail = malloc(tail_len);
  if (!tail)
    return EXT_PLUGIN_NOMEM;
  size_t got = 0;
  ext_plugin_status s = ext_fs_in_read(in, size - tail_len, tail, tail_len, &got);
  if (!s && got != tail_len)
    s = EXT_PLUGIN_INVALID;
  size_t end = tail_len;
  if (!s) {
    for (size_t i = tail_len - ZIP_END + 1; i-- > 0;) {
      if (le32(tail + i) == 0x06054b50u &&
          i + ZIP_END + le16(tail + i + 20) <= tail_len) {
        end = i;
        break;
      }
    }
    if (end == tail_len)
      s = EXT_PLUGIN_INVALID;
  }
  uint32_t count = 0, dir_size = 0, dir_offset = 0;
  if (!s) {
    count = le16(tail + end + 10);
    dir_size = le32(tail + end + 12);
    dir_offset = le32(tail + end + 16);
    uint64_t end_at = size - tail_len + end;
    if (count == 0xffffu || dir_size == 0xffffffffu || dir_offset == 0xffffffffu ||
        (uint64_t)dir_offset + dir_size > end_at)
      s = EXT_PLUGIN_INVALID;
  }
  free(tail);
  uint8_t *dir = NULL;
  if (!s) {
    dir = malloc(dir_size ? dir_size : 1);
    if (!dir)
      s = EXT_PLUGIN_NOMEM;
  }
  if (!s) {
    s = ext_fs_in_read(in, dir_offset, dir, dir_size, &got);
    if (!s && got != dir_size)
      s = EXT_PLUGIN_INVALID;
  }
  size_t at = 0;
  for (uint32_t i = 0; !s && i < count; i++) {
    if (dir_size - at < ZIP_CENTRAL || le32(dir + at) != 0x02014b50u) {
      s = EXT_PLUGIN_INVALID;
      break;
    }
    const uint8_t *h = dir + at;
    uint16_t made_by = le16(h + 4), flags = le16(h + 8), method = le16(h + 10);
    uint32_t crc = le32(h + 16), packed = le32(h + 20), plain = le32(h + 24);
    size_t name_len = le16(h + 28), extra_len = le16(h + 30),
           comment_len = le16(h + 32);
    uint32_t attributes = le32(h + 38), local = le32(h + 42);
    if (dir_size - at - ZIP_CENTRAL < name_len + extra_len + comment_len ||
        memchr(h + ZIP_CENTRAL, 0, name_len)) {
      s = EXT_PLUGIN_INVALID;
      break;
    }
    char *name = field_text(h + ZIP_CENTRAL, name_len);
    if (!name) {
      s = EXT_PLUGIN_NOMEM;
      break;
    }
    at += ZIP_CENTRAL + name_len + extra_len + comment_len;
    /* What the entry is, as Go's archive/zip reads it: Unix modes from a Unix
     * or macOS writer, the DOS directory bit from a DOS or Windows one, and a
     * name ending in a slash is a directory whoever wrote it. */
    int directory = name_len && name[name_len - 1] == '/', other = 0,
        executable = 0;
    uint8_t creator = (uint8_t)(made_by >> 8);
    if (creator == 3 || creator == 19) {
      uint32_t mode = attributes >> 16, type = mode & 0170000u;
      if (type == 0040000u)
        directory = 1;
      else if (type && type != 0100000u)
        other = 1;
      executable = (mode & 0100) != 0;
    } else if ((creator == 0 || creator == 11 || creator == 14) &&
               (attributes & 0x10))
      directory = 1;
    if (directory)
      s = extract_directory(x, name);
    else if (other || (flags & 1) || packed == 0xffffffffu ||
             plain == 0xffffffffu || local == 0xffffffffu ||
             (method != 0 && method != 8))
      s = EXT_PLUGIN_INVALID; /* a link, an encrypted, zip64 or otherwise packed entry */
    else {
      uint8_t header[ZIP_LOCAL];
      s = ext_fs_in_read(in, local, header, ZIP_LOCAL, &got);
      if (!s && (got != ZIP_LOCAL || le32(header) != 0x04034b50u))
        s = EXT_PLUGIN_INVALID;
      uint64_t data = (uint64_t)local + ZIP_LOCAL + le16(header + 26) +
                      le16(header + 28);
      if (!s && data + packed > size)
        s = EXT_PLUGIN_INVALID;
      if (!s) {
        span raw;
        span_init(&raw, in, data, data + packed);
        uint32_t sum = 0;
        if (method == 0)
          s = extract_file(x, name, executable, plain, &raw.base, &sum);
        else {
          inflater *z = inflate_open(&raw.base);
          if (!z)
            s = EXT_PLUGIN_NOMEM;
          else {
            s = extract_file(x, name, executable, plain, &z->base, &sum);
            free(z);
          }
        }
        if (!s && sum != crc)
          s = EXT_PLUGIN_INVALID;
      }
    }
    free(name);
  }
  free(dir);
  return s;
}

/* ---- formats ---- */

typedef ext_plugin_status (*format_reader)(ext_fs_in *in, uint64_t size, extraction *x);
static format_reader detect(const uint8_t *head, size_t len) {
  if (len >= 4 && (!memcmp(head, "PK\x03\x04", 4) || !memcmp(head, "PK\x05\x06", 4)))
    return read_zip;
  if (len >= 2 && head[0] == 0x1f && head[1] == 0x8b)
    return read_tar_gzip;
  if (len >= 262 && !memcmp(head + 257, "ustar", 5))
    return read_tar_plain;
  return NULL;
}

ext_plugin_status ext_store_find_manifest(ext_fs *fs, const char *dir,
    const ext_plugin_store_layout *layout, const char **selected) {
  const char *paths[] = {layout->manifest_path, layout->fallback_path};
  for (size_t i = 0; i < 2 && paths[i]; i++) {
    char *path = *dir ? join2(dir, paths[i]) : strdup(paths[i]);
    ext_fs_kind kind = EXT_FS_NONE;
    ext_plugin_status s = path ? ext_fs_kind_of(fs, path, &kind) : EXT_PLUGIN_NOMEM;
    free(path);
    if (s)
      return s;
    if (kind == EXT_FS_NONE)
      continue;
    if (kind != EXT_FS_FILE)
      return EXT_PLUGIN_INVALID;
    *selected = paths[i];
    return EXT_PLUGIN_OK;
  }
  return EXT_PLUGIN_MISSING;
}

/* The package directory in what was extracted: where the manifest is. */
static ext_plugin_status find_root(ext_fs *fs, const char *dest,
    const ext_plugin_store_layout *layout, char **root) {
  const char *selected = NULL;
  ext_plugin_status s = ext_store_find_manifest(fs, dest, layout, &selected);
  if (s == EXT_PLUGIN_OK) {
    *root = strdup(dest);
    return *root ? EXT_PLUGIN_OK : EXT_PLUGIN_NOMEM;
  }
  if (s != EXT_PLUGIN_MISSING)
    return s;
  ext_fs_entry *entries = NULL;
  size_t count = 0;
  s = ext_fs_list(fs, dest, &entries, &count);
  if (!s && (count != 1 || entries[0].kind != EXT_FS_DIR))
    s = EXT_PLUGIN_INVALID;
  char *only = NULL;
  if (!s) {
    only = join2(dest, entries[0].name);
    s = only ? ext_store_find_manifest(fs, only, layout, &selected) : EXT_PLUGIN_NOMEM;
  }
  ext_fs_list_free(entries, count);
  if (s) {
    free(only);
    return s;
  }
  *root = only;
  return EXT_PLUGIN_OK;
}

ext_plugin_status ext_archive_extract(const char *archive, const char *digest,
                               ext_fs *fs, const char *dest,
                               const ext_archive_limits *limits,
                               const ext_plugin_store_layout *layout, char **root) {
  if (!archive || !*archive || !fs || !dest || !limits || !root)
    return EXT_PLUGIN_INVALID;
  *root = NULL;
  ext_fs_in *in = NULL;
  uint64_t size = 0;
  ext_plugin_status s = ext_fs_in_open(archive, &in, &size);
  if (s)
    return s;
  if (size > limits->max_archive_bytes)
    s = EXT_PLUGIN_INVALID;
  uint8_t head[BLOCK];
  size_t head_len = 0;
  uint8_t *buffer = s ? NULL : malloc(65536);
  if (!s && !buffer)
    s = EXT_PLUGIN_NOMEM;
  if (!s) {
    /* The digest is of the bytes that are about to be read, all of them. */
    ext_hash h;
    ext_hash_init(&h);
    for (uint64_t at = 0; !s && at < size;) {
      size_t got = 0;
      s = ext_fs_in_read(in, at, buffer, 65536, &got);
      if (!s && !got)
        s = EXT_PLUGIN_INVALID; /* the file shrank */
      if (!s) {
        if (!at) {
          head_len = got < BLOCK ? got : BLOCK;
          memcpy(head, buffer, head_len);
        }
        ext_hash_update(&h, buffer, got);
        at += got;
      }
    }
    if (!s && digest) {
      uint8_t sum[32];
      char text[65];
      ext_hash_finish(&h, sum);
      ext_hash_hex(sum, text);
      if (strcmp(text, digest))
        s = EXT_PLUGIN_MISMATCH;
    }
  }
  free(buffer);
  format_reader reader = NULL;
  if (!s) {
    reader = detect(head, head_len);
    if (!reader)
      s = EXT_PLUGIN_UNSUPPORTED;
  }
  if (!s) {
    extraction x = {0};
    x.fs = fs;
    x.dest = dest;
    x.limits = *limits;
    s = reader(in, size, &x);
    seen_free(&x);
  }
  ext_fs_in_close(in);
  if (!s)
    s = find_root(fs, dest, layout, root);
  /* A file that could not be read as an archive, or written where the archive
   * said, is the archive's fault, not a missing package. */
  if (s == EXT_PLUGIN_MISSING)
    s = EXT_PLUGIN_INVALID;
  return s;
}
