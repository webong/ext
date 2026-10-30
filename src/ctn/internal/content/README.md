# CTN content verification and catalog

`src/ctn/internal/content` owns CTN's content verification workflow and durable
file catalog. It is private to CTN, not a separately versioned library. The app
chooses an existing protected root, or creates one with `InitCatalog`. This
package does not select a home directory, network service,
backend, or content policy.

An import streams a regular local file into a private staging directory, records
its SHA-256, byte size, media type, source name, and import time, then publishes
the item atomically. `Inspect` and `List` rehash stored bytes and fail on a
changed record, extra file, link, or payload. `Remove` accepts an explicit item
ID and can remove a damaged item. Imports are bounded to 4 GiB without loading
the whole file into memory.

CTN exposes the same operations:

```sh
ctn content init /absolute/catalog
ctn content import /absolute/catalog /absolute/note.txt
ctn content list /absolute/catalog
ctn content inspect /absolute/catalog ITEM_ID
ctn content verify /absolute/catalog ITEM_ID
ctn content remove /absolute/catalog ITEM_ID
```

Import and inspection return JSON records. The catalog stores local bytes and
metadata only; interpretation, indexing, remote sync, and access policy remain
consumer choices.
