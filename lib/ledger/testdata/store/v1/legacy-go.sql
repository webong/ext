-- Frozen database written by the retired Go SQLite store before removal.
BEGIN TRANSACTION;
CREATE TABLE "changes" (
  "cursor" INTEGER NOT NULL,
  "namespace" TEXT NOT NULL,
  "revision" INTEGER NOT NULL,
  "committed_at" TEXT NOT NULL,
  "size" INTEGER NOT NULL,
  "body" TEXT NOT NULL,
  PRIMARY KEY ("cursor")
);
INSERT INTO "changes" VALUES(1,'cross.example',1,'2026-11-25T18:07:35.251434Z',604,'{"committed_at":"2026-11-25T18:07:35.251434Z","cursor":1,"namespace":"cross.example","revision":1,"vertices":[{"attributes":{"list":["x",true,null],"n":1.5},"created_at":"2026-11-25T18:07:35.251434Z","id":"a","kind":"cross.example/node","labels":{"tier":"gold"},"namespace":"cross.example","provenance":{"at":"2026-01-02T03:04:05.0000006+01:00","operation":"write","source":"go"},"revision":1,"updated_at":"2026-11-25T18:07:35.251434Z"},{"created_at":"2026-11-25T18:07:35.251434Z","id":"b","kind":"cross.example/node","namespace":"cross.example","revision":1,"updated_at":"2026-11-25T18:07:35.251434Z"}]}');
INSERT INTO "changes" VALUES(2,'cross.example',2,'2026-11-25T18:07:35.251947Z',316,'{"committed_at":"2026-11-25T18:07:35.251947Z","cursor":2,"edges":[{"attributes":{"w":2},"created_at":"2026-11-25T18:07:35.251947Z","from":"a","id":"ab","namespace":"cross.example","revision":2,"to":"b","type":"cross.example/link","updated_at":"2026-11-25T18:07:35.251947Z"}],"namespace":"cross.example","revision":2}');
CREATE TABLE "edges" (
  "namespace" TEXT NOT NULL,
  "id" TEXT NOT NULL,
  "type" TEXT NOT NULL,
  "from_id" TEXT NOT NULL,
  "to_id" TEXT NOT NULL,
  "revision" INTEGER NOT NULL,
  "created_at" TEXT NOT NULL,
  "updated_at" TEXT NOT NULL,
  "labels" TEXT,
  "attributes" TEXT,
  "provenance" TEXT,
  PRIMARY KEY ("namespace", "id"),
  FOREIGN KEY ("namespace", "from_id") REFERENCES "vertices" ("namespace", "id"),
  FOREIGN KEY ("namespace", "to_id") REFERENCES "vertices" ("namespace", "id")
) WITHOUT ROWID;
INSERT INTO "edges" VALUES('cross.example','ab','cross.example/link','a','b',2,'2026-11-25T18:07:35.251947Z','2026-11-25T18:07:35.251947Z',NULL,'{"w":2}',NULL);
CREATE TABLE "idempotency" (
  "namespace" TEXT NOT NULL,
  "key" TEXT NOT NULL,
  "fingerprint" TEXT NOT NULL,
  "revision" INTEGER NOT NULL,
  "cursor" INTEGER NOT NULL,
  "committed_at" TEXT NOT NULL,
  PRIMARY KEY ("namespace", "key")
) WITHOUT ROWID;
INSERT INTO "idempotency" VALUES('cross.example','go-1','c3622d386a06d6d920b20abb0a6d7741cf61cbcebbc6d7cecc4ec9be56a1776d',1,1,'2026-11-25T18:07:35.251434Z');
CREATE TABLE "namespaces" (
  "namespace" TEXT NOT NULL,
  "schema_version" TEXT NOT NULL,
  "schema" TEXT NOT NULL,
  "revision" INTEGER NOT NULL,
  PRIMARY KEY ("namespace")
) WITHOUT ROWID;
INSERT INTO "namespaces" VALUES('cross.example','1','{"namespace":"cross.example","version":"1"}',2);
CREATE TABLE "store" (
  "id" INTEGER NOT NULL CHECK (id = 1),
  "layout" TEXT NOT NULL,
  "cursor" INTEGER NOT NULL,
  "history_floor" INTEGER NOT NULL,
  PRIMARY KEY ("id")
);
INSERT INTO "store" VALUES(1,'ext.graph.store/v1',2,0);
CREATE TABLE "vertices" (
  "namespace" TEXT NOT NULL,
  "id" TEXT NOT NULL,
  "kind" TEXT NOT NULL,
  "revision" INTEGER NOT NULL,
  "created_at" TEXT NOT NULL,
  "updated_at" TEXT NOT NULL,
  "labels" TEXT,
  "attributes" TEXT,
  "provenance" TEXT,
  PRIMARY KEY ("namespace", "id"),
  FOREIGN KEY ("namespace") REFERENCES "namespaces" ("namespace")
) WITHOUT ROWID;
INSERT INTO "vertices" VALUES('cross.example','a','cross.example/node',1,'2026-11-25T18:07:35.251434Z','2026-11-25T18:07:35.251434Z','{"tier":"gold"}','{"list":["x",true,null],"n":1.5}','{"at":"2026-01-02T03:04:05.0000006+01:00","operation":"write","source":"go"}');
INSERT INTO "vertices" VALUES('cross.example','b','cross.example/node',1,'2026-11-25T18:07:35.251434Z','2026-11-25T18:07:35.251434Z',NULL,NULL,NULL);
CREATE INDEX "vertices_by_kind" ON "vertices" ("namespace", "kind", "id");
CREATE INDEX "edges_by_type" ON "edges" ("namespace", "type", "id");
CREATE INDEX "edges_by_from" ON "edges" ("namespace", "from_id", "id");
CREATE INDEX "edges_by_to" ON "edges" ("namespace", "to_id", "id");
CREATE INDEX "changes_by_namespace" ON "changes" ("namespace", "cursor");
CREATE INDEX "idempotency_by_cursor" ON "idempotency" ("cursor");
COMMIT;
