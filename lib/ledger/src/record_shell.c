/* Shell sessions and web origins: observe_shell and observe_web
 * (recorder/v1). The recorder never reads environment values other than the
 * few the contract names, nor shell history. */
#include "recorder.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

/* The nearest directory from cwd up that holds a project marker, else cwd. */
static char *project_root(const rec *r, const char *cwd) {
  static const char *const markers[] = {".git", ".ctx", "go.mod", "package.json"};
  char *dir = rec_clean(cwd);
  while (dir) {
    for (size_t i = 0; i < sizeof markers / sizeof *markers; i++) {
      char *p = rec_join(dir, markers[i]);
      int found = p && rec_exists(r, p);
      free(p);
      if (found) return dir;
    }
    char *parent = rec_dir(dir);
    if (!parent || strcmp(parent, dir) == 0) {
      free(parent);
      free(dir);
      return rec_clean(cwd);
    }
    free(dir);
    dir = parent;
  }
  return NULL;
}

static int push(lj_value *array, lj_value *item) { return item ? lj_array_push(array, item) : -1; }

static void id_of(char *out, size_t n, const char *prefix, const char *value) {
  char d[25];
  rec_digest(value, strlen(value), d);
  snprintf(out, n, "%s%s", prefix, d);
}

int rec_observe_shell(rec *r, const ext_ledger_context *ctx, const lj_value *in) {
  const char *given = rec_field(in, "cwd"), *profile = rec_field(in, "profile");
  char *cwd = rec_clean(*given ? given : r->env.wd);
  if (cwd && !rec_is_abs(cwd)) {
    char *abs = rec_join(r->env.wd, cwd);
    free(cwd);
    cwd = abs;
  }
  char *project = cwd ? project_root(r, cwd) : NULL;
  const char *shell_var = rec_getenv(r, "SHELL");
  if (!*shell_var) shell_var = rec_getenv(r, "ComSpec");
  char *shell = rec_base(shell_var);
  char pid[24], session[48], s[160];
  rec_itoa(r->env.ppid, pid);
  snprintf(session, sizeof session, "shell/%s", pid);
  lj_buf now = {0};
  rec_now_text(r, &now);
  int rc = (!cwd || !project || !shell || now.oom) ? EXT_LEDGER_IO : 0;

  char dir_id[64], project_id[64], profile_id[64], dir_digest[25];
  if (!rc) {
    id_of(dir_id, sizeof dir_id, "directory/", cwd);
    id_of(project_id, sizeof project_id, "project/", project);
    id_of(profile_id, sizeof profile_id, "profile/", profile);
    rec_digest(cwd, strlen(cwd), dir_digest);
  }
  lj_value *tx = rec_obj(), *vertices = lj_new(LJ_ARRAY), *edges = lj_new(LJ_ARRAY), *deletes = lj_new(LJ_ARRAY);
  lj_value *session_attrs = rec_obj(), *selection_edges = lj_new(LJ_ARRAY);
  if (!tx || !vertices || !edges || !deletes || !session_attrs || !selection_edges) rc = EXT_LEDGER_IO;
  if (!rc) {
    lj_value *machine = rec_obj();
    rc |= rec_set(machine, "hostname", rec_str(r->env.hostname)) | rec_set(machine, "os", rec_str(r->env.os)) |
          rec_set(machine, "architecture", rec_str(r->env.arch));
    rc |= push(vertices, rec_vertex(r, "machine/local", "machine", machine, "shell-observed"));
    rc |= rec_set(session_attrs, "parent_pid", rec_num((double)r->env.ppid)) | rec_set(session_attrs, "shell", rec_str(shell)) |
          rec_set(session_attrs, "terminal", rec_str(rec_getenv(r, "TERM_PROGRAM"))) | rec_set(session_attrs, "working_directory", rec_str(cwd)) |
          rec_set(session_attrs, "active_profile", rec_str(profile)) | rec_set(session_attrs, "observed_at", rec_strn(now.ptr, now.len));
    rc |= push(vertices, rec_vertex(r, session, "shell-session", lj_clone(session_attrs), "shell-observed"));
    lj_value *dir = rec_obj(), *proj = rec_obj();
    rc |= rec_set(dir, "path", rec_str(cwd)) | rec_set(proj, "path", rec_str(project));
    rc |= push(vertices, rec_vertex(r, dir_id, "directory", dir, "directory-observed"));
    rc |= push(vertices, rec_vertex(r, project_id, "project", proj, "project-observed"));
    snprintf(s, sizeof s, "machine-shell/%s", pid);
    rc |= push(edges, rec_edge(s, "machine/local", session, "hosts"));
    snprintf(s, sizeof s, "shell-directory/%s", pid);
    rc |= push(edges, rec_edge(s, session, dir_id, "working-in"));
    snprintf(s, sizeof s, "directory-project/%s", dir_digest);
    rc |= push(edges, rec_edge(s, dir_id, project_id, "within"));
    if (*profile) {
      lj_value *p = rec_obj();
      rc |= rec_set(p, "name", rec_str(profile));
      rc |= push(vertices, rec_vertex(r, profile_id, "profile", p, "profile-observed"));
      snprintf(s, sizeof s, "shell-profile/%s", pid);
      rc |= push(edges, rec_edge(s, session, profile_id, "uses-profile"));
    }
  }
  const lj_value *selections = lj_get(in, "selections");
  for (size_t i = 0; !rc && selections && selections->type == LJ_OBJECT && i < selections->u.object.len; i++) {
    const lj_member *m = &selections->u.object.members[i];
    if (m->value->type != LJ_STRING) continue;
    const lj_str *key = &m->key, *value = &m->value->u.string;
    if (key->len == 0 || strcmp(key->ptr, "profile") == 0 || value->len == 0) continue;
    lj_buf kv = {0};
    lj_buf_put(&kv, key->ptr, key->len);
    lj_buf_putc(&kv, 0);
    lj_buf_put(&kv, value->ptr, value->len);
    char d[25], vd[25], kd[25], selection[64];
    rec_digest(kv.ptr ? kv.ptr : "", kv.len, d);
    rec_digest(value->ptr, value->len, vd);
    rec_digest(key->ptr, key->len, kd);
    lj_buf_free(&kv);
    snprintf(selection, sizeof selection, "selection/%s", d);
    lj_value *attrs = rec_obj();
    rc |= rec_set(attrs, "key", rec_strn(key->ptr, key->len)) | rec_set(attrs, "value_digest", rec_str(vd));
    if (rec_context_name(value->ptr, value->len)) rc |= rec_set(attrs, "value", rec_strn(value->ptr, value->len));
    else rc |= rec_set(attrs, "value_redacted", rec_bool(1));
    rc |= push(vertices, rec_vertex(r, selection, "selection", attrs, "selection-observed"));
    snprintf(s, sizeof s, "shell-selection/%s/%s", pid, kd);
    rc |= push(selection_edges, rec_edge(s, session, selection, "selects"));
  }
  for (size_t i = 0; !rc && i < selection_edges->u.array.len; i++) rc |= push(edges, lj_clone(selection_edges->u.array.items[i]));
  rc = rc ? EXT_LEDGER_IO : 0;

  /* What is already recorded: the profile edge, the selections, the session. */
  char profile_edge[64];
  snprintf(profile_edge, sizeof profile_edge, "shell-profile/%s", pid);
  lj_value *old_profile = NULL, *old_selections = NULL, *existing = NULL;
  int has_old_profile = 0;
  if (!rc) {
    int got = rec_get(r, ctx, 1, profile_edge, &old_profile);
    if (got && got != EXT_LEDGER_NOT_FOUND) rc = got;
    has_old_profile = got == 0;
  }
  if (!rc && !*profile && has_old_profile) {
    lj_value *ref = rec_obj();
    rc = (rec_set(ref, "namespace", rec_str(REC_NS)) | rec_set(ref, "id", rec_str(profile_edge)) | push(deletes, ref)) ? EXT_LEDGER_IO : 0;
  }
  if (!rc) rc = rec_query_edges(r, ctx, "selects", session, 256, &old_selections);
  for (size_t i = 0; !rc && i < old_selections->u.array.len; i++) {
    const char *id = rec_field(old_selections->u.array.items[i], "id");
    int current = 0;
    for (size_t k = 0; k < selection_edges->u.array.len && !current; k++) current = strcmp(rec_field(selection_edges->u.array.items[k], "id"), id) == 0;
    if (current) continue;
    lj_value *ref = rec_obj();
    rc = (rec_set(ref, "namespace", rec_str(REC_NS)) | rec_set(ref, "id", rec_str(id)) | push(deletes, ref)) ? EXT_LEDGER_IO : 0;
  }
  int unchanged = 0;
  if (!rc) {
    int got = rec_get(r, ctx, 0, session, &existing);
    if (got && got != EXT_LEDGER_NOT_FOUND) rc = got;
    if (!got && rec_equal_attributes(lj_get(existing, "attributes"), session_attrs)) {
      unchanged = old_selections->u.array.len == selection_edges->u.array.len;
      for (size_t k = 0; unchanged && k < selection_edges->u.array.len; k++) {
        const lj_value *e = selection_edges->u.array.items[k];
        int found = 0;
        for (size_t o = 0; o < old_selections->u.array.len && !found; o++) {
          const lj_value *old = old_selections->u.array.items[o];
          found = strcmp(rec_field(old, "id"), rec_field(e, "id")) == 0 && strcmp(rec_field(old, "to"), rec_field(e, "to")) == 0;
        }
        unchanged = found;
      }
      if (unchanged)
        unchanged = (!*profile && !has_old_profile) || (*profile && has_old_profile && strcmp(rec_field(old_profile, "to"), profile_id) == 0);
    }
  }
  if (!rc && !unchanged) {
    int bad = rec_set(tx, "namespace", rec_str(REC_NS)) | rec_set(tx, "vertices", vertices) | rec_set(tx, "edges", edges);
    vertices = edges = NULL;
    if (deletes->u.array.len) {
      bad |= rec_set(tx, "delete_edges", deletes);
      deletes = NULL;
    }
    rc = bad ? EXT_LEDGER_IO : rec_apply(r, ctx, tx);
    tx = NULL;
  }
  lj_free(tx);
  lj_free(vertices);
  lj_free(edges);
  lj_free(deletes);
  lj_free(session_attrs);
  lj_free(selection_edges);
  lj_free(old_profile);
  lj_free(old_selections);
  lj_free(existing);
  lj_buf_free(&now);
  free(cwd);
  free(project);
  free(shell);
  return rc;
}

int rec_observe_web(rec *r, const ext_ledger_context *ctx, const lj_value *in) {
  const char *host = rec_field(in, "host");
  if (!*host) return 0;
  char pid[24], session[48], edge_id[64];
  rec_itoa(r->env.ppid, pid);
  snprintf(session, sizeof session, "shell/%s", pid);
  snprintf(edge_id, sizeof edge_id, "shell-browser/%s", pid);
  lj_buf now = {0};
  rec_now_text(r, &now);
  lj_value *attrs = rec_obj();
  int rc = (rec_set(attrs, "provider", rec_str(rec_field(in, "provider"))) | rec_set(attrs, "profile", rec_str(rec_field(in, "profile"))) |
            rec_set(attrs, "scheme", rec_str(rec_field(in, "scheme"))) | rec_set(attrs, "host", rec_str(host)) |
            rec_set(attrs, "observed_at", rec_strn(now.ptr, now.len)))
               ? EXT_LEDGER_IO
               : 0;
  lj_buf_free(&now);
  lj_value *existing = NULL, *old = NULL;
  if (!rc && rec_get(r, ctx, 0, "browser/current", &existing) == 0 && rec_equal_attributes(lj_get(existing, "attributes"), attrs) &&
      rec_get(r, ctx, 1, edge_id, &old) == 0 && strcmp(rec_field(old, "from"), session) == 0 && strcmp(rec_field(old, "to"), "browser/current") == 0) {
    lj_free(existing);
    lj_free(old);
    lj_free(attrs);
    return 0;
  }
  lj_free(existing);
  lj_free(old);
  if (rc) {
    lj_free(attrs);
    return rc;
  }
  lj_value *tx = rec_obj(), *vertices = lj_new(LJ_ARRAY), *edges = lj_new(LJ_ARRAY);
  int bad = !vertices || !edges;
  bad |= push(vertices, rec_vertex(r, "browser/current", "browser-context", attrs, "web-origin-observed"));
  bad |= push(edges, rec_edge(edge_id, session, "browser/current", "opened"));
  bad |= rec_set(tx, "namespace", rec_str(REC_NS)) | rec_set(tx, "vertices", vertices) | rec_set(tx, "edges", edges);
  if (bad) {
    lj_free(tx);
    return EXT_LEDGER_IO;
  }
  return rec_apply(r, ctx, tx);
}
