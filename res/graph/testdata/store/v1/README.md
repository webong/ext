# Graph store contract, version 1

What a `res/graph` store holds, the rules it applies, how it lays out a durable graph and how it
answers each operation. The store is generic: namespaces, kinds and relationship types are the
consumer's vocabulary.

| File | What it fixes |
| --- | --- |
| `entities.schema.json` | The wire form of every entity (JSON Schema 2020-12): vertex, edge, provenance, reference, schema, relationship, transaction, commit, change, snapshot, path, the two queries and the store options |
| `rules.json` | The naming, secret and size rules, the default bounds, the order in which a transaction is checked, and what each error code means here |
| `tables.json` | The SQLite layout of a durable store, from which the store creates its tables |
| `cases.json` | The operation cases |
| `fingerprints.json` | Canonical JSON and SHA-256 of transactions, which idempotency keys bind to |

## Cases

Each case runs its `steps` in order against a new, empty store opened with the case's `options`
(absent bounds take the defaults in `rules.json`). A case with `"durable": true` runs only on a
store that persists, because it reopens the store.

| `op` | Arguments | Returns, for `expect` |
| --- | --- | --- |
| `register` | `schema` | nothing |
| `apply` | `transaction` | the commit |
| `get_vertex`, `get_edge` | `namespace`, `id` | the record |
| `query_vertices`, `query_edges` | `query` | the records, in ID order |
| `traverse` | `namespace`, `start`, `max_depth`, `max_vertices` | the paths |
| `snapshot` | `namespace` | the snapshot |
| `changes` | `namespace` (empty for all), `after`, `limit` | the changes, in cursor order |
| `watch` | `namespace`, `after`, `buffer` | the changes the watch replays at once; the runner then cancels it |
| `reopen` | none | closes the store and opens it again on the same storage |

`error` is the code the step must fail with; without it the step must succeed. `expect`, when
present, is matched against the result's JSON form:

- an object matches when every field it names matches; fields it does not name are not checked;
- a field absent from the result matches `null`, `false`, `0`, `""` or `[]`;
- a string in place of an object matches that object's `id`, so `"vertices": ["a", "b"]` names
  records by ID, and a path is `{"vertices": [ids], "edges": [ids]}`;
- two RFC 3339 strings match when they name the same instant;
- an array matches element by element and must have the same length.

Commit times are not fixed by the cases; `created_at` is, because a transaction may supply it for
a new record and a store keeps it on every later write.

## Behaviour the cases cannot hold

Live delivery is tested by each implementation: a watch delivers later commits in cursor order,
closes when its buffer fills, its context ends or the store closes, and closes when its cursor
falls below the history floor. Concurrent writers, including other processes on one durable store,
lose no commit. A closed store answers `unavailable`, a canceled context `canceled` and an expired
one `timeout`, before any other check. In Go these are `storetest.RunBehaviour` and the
multi-process tests of `store/sqlite`.

## Canonical JSON

Idempotency fingerprints, stored schemas and stored changes use canonical JSON (after RFC 8785):

- the value's wire form, with empty and zero fields omitted; `expected_revision` is kept whenever it
  is present, even at 0, and times print in RFC 3339 with the offset they were given and no
  trailing zeros in the fraction;
- object keys sorted by their UTF-16 code units, at every depth;
- no insignificant whitespace;
- numbers in the shortest form that round-trips, as ECMAScript prints them (`1`, `1e+21`, `1e-7`);
- strings with only `"`, `\` and control characters escaped (`\b \t \n \f \r` and `\u00xx`
  lowercase), everything else as UTF-8.

The fingerprint is the lowercase hex SHA-256 of those bytes. A change's size, which counts against
`max_change_bytes`, is the length of its canonical JSON.

## Durable layout

A durable store keeps one SQLite database laid out as `tables.json` says, with the pragmas it
names. It creates the tables and indexes from that file when the database is new (in the order
listed, inside one write transaction), stores the layout name in the one row of `store`, and
refuses a database whose `store` row names another layout or that holds unrelated tables. There
are no migrations. Times are RFC 3339 text in UTC. `labels`, `attributes` and `provenance` hold
canonical JSON or NULL when empty; `changes.body` holds the canonical JSON of the change entity,
and `namespaces.schema` that of the normalised schema (kinds sorted and deduplicated,
relationships sorted by type, then from, then to). A commit takes the write lock first
(`BEGIN IMMEDIATE`), so two writers never interleave.

## Not covered

Replication, migrations from any earlier format (the JSON snapshot store is gone; its file is not
read), indexes beyond `tables.json`, full-text or attribute queries, and the recorder's `ext.system`
vocabulary, which is the recorder's own schema (`graph.Schema()`), not part of this contract.
