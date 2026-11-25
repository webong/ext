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
INSERT INTO "changes" VALUES(1,'ext.system',1,'2026-11-25T18:07:34.854317Z',283,'{"committed_at":"2026-11-25T18:07:34.854317Z","cursor":1,"namespace":"ext.system","revision":1,"vertices":[{"created_at":"2026-11-25T18:07:34.854317Z","id":"saved-machine","kind":"ext.system/machine","namespace":"ext.system","revision":1,"updated_at":"2026-11-25T18:07:34.854317Z"}]}');
INSERT INTO "changes" VALUES(2,'external',1,'2026-11-25T18:07:34.854863Z',658,'{"committed_at":"2026-11-25T18:07:34.854863Z","cursor":2,"edges":[{"created_at":"2026-11-25T18:07:34.854863Z","from":"saved","id":"edge","namespace":"external","revision":1,"to":"other","type":"external/link","updated_at":"2026-11-25T18:07:34.854863Z"}],"namespace":"external","revision":1,"vertices":[{"attributes":{"value":"preserved"},"created_at":"2026-11-25T18:07:34.854863Z","id":"saved","kind":"external/fact","namespace":"external","revision":1,"updated_at":"2026-11-25T18:07:34.854863Z"},{"created_at":"2026-11-25T18:07:34.854863Z","id":"other","kind":"external/fact","namespace":"external","revision":1,"updated_at":"2026-11-25T18:07:34.854863Z"}]}');
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
INSERT INTO "edges" VALUES('external','edge','external/link','saved','other',1,'2026-11-25T18:07:34.854863Z','2026-11-25T18:07:34.854863Z',NULL,NULL,NULL);
CREATE TABLE "idempotency" (
  "namespace" TEXT NOT NULL,
  "key" TEXT NOT NULL,
  "fingerprint" TEXT NOT NULL,
  "revision" INTEGER NOT NULL,
  "cursor" INTEGER NOT NULL,
  "committed_at" TEXT NOT NULL,
  PRIMARY KEY ("namespace", "key")
) WITHOUT ROWID;
CREATE TABLE "namespaces" (
  "namespace" TEXT NOT NULL,
  "schema_version" TEXT NOT NULL,
  "schema" TEXT NOT NULL,
  "revision" INTEGER NOT NULL,
  PRIMARY KEY ("namespace")
) WITHOUT ROWID;
INSERT INTO "namespaces" VALUES('ext.system','1','{"kinds":["ext.system/accessibility-element","ext.system/adapter","ext.system/alias","ext.system/application","ext.system/browser-context","ext.system/capability","ext.system/context","ext.system/desktop","ext.system/directory","ext.system/display","ext.system/executable","ext.system/filesystem","ext.system/host-inventory","ext.system/host-resource","ext.system/installer","ext.system/inventory","ext.system/machine","ext.system/permission","ext.system/process","ext.system/process-resource","ext.system/profile","ext.system/program","ext.system/project","ext.system/resource","ext.system/selection","ext.system/shell","ext.system/shell-session","ext.system/support","ext.system/webview","ext.system/window"],"namespace":"ext.system","relationships":[{"from":"ext.system/process","to":"ext.system/application","type":"ext.system/belongs-to-application"},{"from":"ext.system/context","to":"ext.system/resource","type":"ext.system/contains"},{"from":"ext.system/process","to":"ext.system/executable","type":"ext.system/executes"},{"from":"ext.system/machine","to":"ext.system/accessibility-element","type":"ext.system/has-accessibility-element"},{"from":"ext.system/machine","to":"ext.system/adapter","type":"ext.system/has-adapter"},{"from":"ext.system/machine","to":"ext.system/desktop","type":"ext.system/has-desktop"},{"from":"ext.system/machine","to":"ext.system/display","type":"ext.system/has-display"},{"from":"ext.system/machine","to":"ext.system/filesystem","type":"ext.system/has-filesystem"},{"from":"ext.system/machine","to":"ext.system/host-inventory","type":"ext.system/has-host-inventory"},{"from":"ext.system/machine","to":"ext.system/host-resource","type":"ext.system/has-host-resource"},{"from":"ext.system/machine","to":"ext.system/permission","type":"ext.system/has-permission"},{"from":"ext.system/machine","to":"ext.system/program","type":"ext.system/has-program"},{"from":"ext.system/machine","to":"ext.system/shell","type":"ext.system/has-shell"},{"from":"ext.system/machine","to":"ext.system/webview","type":"ext.system/has-webview"},{"from":"ext.system/machine","to":"ext.system/window","type":"ext.system/has-window"},{"from":"ext.system/machine","to":"ext.system/shell-session","type":"ext.system/hosts"},{"from":"ext.system/installer","to":"ext.system/adapter","type":"ext.system/implemented-by"},{"from":"ext.system/program","to":"ext.system/installer","type":"ext.system/installed-by"},{"from":"ext.system/adapter","to":"ext.system/context","type":"ext.system/offers"},{"from":"ext.system/shell-session","to":"ext.system/browser-context","type":"ext.system/opened"},{"from":"ext.system/process","to":"ext.system/process","type":"ext.system/parent-of"},{"from":"ext.system/resource","to":"ext.system/resource","type":"ext.system/relates"},{"from":"ext.system/alias","to":"ext.system/context","type":"ext.system/resolves-to"},{"from":"ext.system/machine","to":"ext.system/process","type":"ext.system/runs"},{"from":"ext.system/shell-session","to":"ext.system/selection","type":"ext.system/selects"},{"from":"ext.system/adapter","to":"ext.system/capability","type":"ext.system/supports"},{"from":"ext.system/adapter","to":"ext.system/support","type":"ext.system/supports-kind"},{"from":"ext.system/shell-session","to":"ext.system/profile","type":"ext.system/uses-profile"},{"from":"ext.system/context","to":"ext.system/program","type":"ext.system/uses-program"},{"from":"ext.system/process","to":"ext.system/process-resource","type":"ext.system/uses-resource"},{"from":"ext.system/directory","to":"ext.system/project","type":"ext.system/within"},{"from":"ext.system/shell-session","to":"ext.system/directory","type":"ext.system/working-in"}],"version":"1"}',1);
INSERT INTO "namespaces" VALUES('external','1','{"namespace":"external","version":"1"}',1);
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
INSERT INTO "vertices" VALUES('ext.system','saved-machine','ext.system/machine',1,'2026-11-25T18:07:34.854317Z','2026-11-25T18:07:34.854317Z',NULL,NULL,NULL);
INSERT INTO "vertices" VALUES('external','other','external/fact',1,'2026-11-25T18:07:34.854863Z','2026-11-25T18:07:34.854863Z',NULL,NULL,NULL);
INSERT INTO "vertices" VALUES('external','saved','external/fact',1,'2026-11-25T18:07:34.854863Z','2026-11-25T18:07:34.854863Z',NULL,'{"value":"preserved"}',NULL);
CREATE INDEX "vertices_by_kind" ON "vertices" ("namespace", "kind", "id");
CREATE INDEX "edges_by_type" ON "edges" ("namespace", "type", "id");
CREATE INDEX "edges_by_from" ON "edges" ("namespace", "from_id", "id");
CREATE INDEX "edges_by_to" ON "edges" ("namespace", "to_id", "id");
CREATE INDEX "changes_by_namespace" ON "changes" ("namespace", "cursor");
CREATE INDEX "idempotency_by_cursor" ON "idempotency" ("cursor");
COMMIT;
