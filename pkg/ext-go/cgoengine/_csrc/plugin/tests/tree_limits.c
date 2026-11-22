/* The directory digest keeps every path until it has sorted them, so it stops
 * a tree that holds too many files or too many path bytes instead of using
 * all the memory it is given. The real caps are large; this build of
 * integrity.c lowers them (see CMakeLists.txt) and drives the collector that
 * the directory walk calls for each file. */
#include "../src/integrity.c"
#include <assert.h>

static void release(listing *list) {
  for (size_t i = 0; i < list->count; i++)
    free(list->files[i].path);
  free(list->files);
  memset(list, 0, sizeof(*list));
}

int main(void) {
  uint8_t sum[32] = {0};
  listing files = {0};
  for (unsigned i = 0; i < EXT_TREE_MAX_FILES; i++)
    assert(collect(&files, "a", sum) == EXT_OK);
  assert(collect(&files, "a", sum) == EXT_INVALID);
  assert(files.count == EXT_TREE_MAX_FILES);
  release(&files);

  /* each path costs its length plus one byte */
  listing bytes = {0};
  char path[EXT_TREE_MAX_PATH_BYTES];
  size_t half = EXT_TREE_MAX_PATH_BYTES / 2;
  memset(path, 'p', half - 1);
  path[half - 1] = 0;
  assert(collect(&bytes, path, sum) == EXT_OK);
  assert(collect(&bytes, path, sum) == EXT_OK);
  assert(collect(&bytes, "x", sum) == EXT_INVALID);
  assert(bytes.count == 2);
  release(&bytes);

  /* a single path larger than the whole budget is refused without overflow */
  listing one = {0};
  char big[EXT_TREE_MAX_PATH_BYTES + 8];
  memset(big, 'p', sizeof(big) - 1);
  big[sizeof(big) - 1] = 0;
  assert(collect(&one, big, sum) == EXT_INVALID);
  assert(one.count == 0);
  release(&one);
  return 0;
}
