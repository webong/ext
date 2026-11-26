/* Portable per-user service definitions. Platform managers consume the
 * rendered definition; this library never invokes launchctl or systemctl. */
#include "collect.h"
#include "recorder.h"

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

static const char *field(const lj_value *v, const char *key) {
  const lj_value *item = lj_get(v, key);
  return item && item->type == LJ_STRING && strlen(item->u.string.ptr) == item->u.string.len ? item->u.string.ptr : NULL;
}

static int text(const char *s, size_t max) {
  if (!s || strlen(s) > max) return 0;
  for (const unsigned char *p = (const unsigned char *)s; *p; p++)
    if (*p < 32 || *p == 127 || (*p == 0xc2 && p[1] >= 0x80 && p[1] <= 0x9f)) return 0;
  return 1;
}

static int service_name(const char *s, int windows) {
  if (!s || !*s || strlen(s) > 128 || s[0] == '.' || s[0] == '-' || s[0] == ':' || s[0] == ' ') return 0;
  size_t n = strlen(s);
  if (s[n - 1] == ' ') return 0;
  for (const unsigned char *p = (const unsigned char *)s; *p; p++)
    if (!( (*p >= 'a' && *p <= 'z') || (*p >= 'A' && *p <= 'Z') || (*p >= '0' && *p <= '9') ||
           *p == '.' || *p == '_' || *p == '-' || *p == ':' || *p == '@' || (windows && *p == ' '))) return 0;
  return 1;
}

static int path(const char *s, int windows) {
  if (!text(s, 4096) || !*s) return 0;
  if (!windows) return s[0] == '/';
  if (strlen(s) < 3 || !(((s[0] | 32) >= 'a' && (s[0] | 32) <= 'z') && s[1] == ':' && (s[2] == '\\' || s[2] == '/'))) return 0;
  for (const char *p = s + 2; *p; p++) if (strchr(":*?\"<>|%", *p)) return 0;
  return 1;
}

static int environment_name(const char *s) {
  if (!s || !*s || strlen(s) > 256) return 0;
  for (size_t i = 0; s[i]; i++) {
    char c = s[i];
    if ((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_' || (i && c >= '0' && c <= '9')) continue;
    return 0;
  }
  return 1;
}

static int cmp_member(const void *a, const void *b) {
  const lj_member *const *x = a, *const *y = b;
  return strcmp((*x)->key.ptr, (*y)->key.ptr);
}

static void xml(lj_buf *b, const char *s) {
  for (const char *p = s; *p; p++) {
    switch (*p) {
      case '&': lj_buf_puts(b, "&amp;"); break;
      case '<': lj_buf_puts(b, "&lt;"); break;
      case '>': lj_buf_puts(b, "&gt;"); break;
      case '\"': lj_buf_puts(b, "&#34;"); break;
      case '\'': lj_buf_puts(b, "&#39;"); break;
      default: lj_buf_putc(b, *p);
    }
  }
}

static void xml_pair(lj_buf *b, const char *key, const char *value) {
  lj_buf_puts(b, "<key>"); lj_buf_puts(b, key); lj_buf_puts(b, "</key><string>");
  xml(b, value); lj_buf_puts(b, "</string>\n");
}

static void systemd_quote(lj_buf *b, const char *s) {
  lj_buf_putc(b, '\"');
  for (const char *p = s; *p; p++) {
    switch (*p) {
      case '\\': lj_buf_puts(b, "\\\\"); break;
      case '\"': lj_buf_puts(b, "\\\""); break;
      case '%': lj_buf_puts(b, "%%"); break;
      case '$': lj_buf_puts(b, "$$"); break;
      case '\t': lj_buf_puts(b, "\\t"); break;
      default: lj_buf_putc(b, *p);
    }
  }
  lj_buf_putc(b, '\"');
}

static void windows_argument(lj_buf *b, const char *s) {
  lj_buf_putc(b, '\"');
  size_t slashes = 0;
  for (const char *p = s; *p; p++) {
    if (*p == '\\') { slashes++; continue; }
    size_t count = *p == '\"' ? slashes * 2 + 1 : slashes;
    for (size_t i = 0; i < count; i++) lj_buf_putc(b, '\\');
    lj_buf_putc(b, *p);
    slashes = 0;
  }
  for (size_t i = 0; i < slashes * 2; i++) lj_buf_putc(b, '\\');
  lj_buf_putc(b, '\"');
}

static int valid(const lj_value *in, const char **platform, const char **name, const char **description,
                 const char **executable, const char **workdir, const char **restart, int64_t *seconds,
                 const char **user, const lj_value **arguments, const lj_value **environment) {
  const lj_value *spec = lj_get(in, "spec");
  *platform = field(in, "platform");
  if (!*platform || (strcmp(*platform, "darwin") && strcmp(*platform, "linux") && strcmp(*platform, "windows"))) return EXT_LEDGER_UNSUPPORTED;
  int windows = !strcmp(*platform, "windows");
  if (!spec || spec->type != LJ_OBJECT) return EXT_LEDGER_INVALID;
  *name = field(spec, "name");
  *description = field(spec, "description");
  *executable = field(spec, "executable");
  *workdir = field(spec, "working_directory");
  *restart = field(spec, "restart");
  *user = field(spec, "user_id");
  *arguments = lj_get(spec, "arguments");
  *environment = lj_get(spec, "environment");
  if (!service_name(*name, windows) || !path(*executable, windows) ||
      (*description && !text(*description, 1024)) || (*workdir && *(*workdir) && !path(*workdir, windows)) ||
      (windows && *user && !text(*user, 256))) return EXT_LEDGER_INVALID;
  if (!*description || !**description) *description = *name;
  if (!*workdir) *workdir = "";
  if (!*restart || !**restart) *restart = "on-failure";
  if (strcmp(*restart, "never") && strcmp(*restart, "on-failure") && strcmp(*restart, "always")) return EXT_LEDGER_INVALID;
  if (!*user) *user = "";
  const lj_value *delay = lj_get(spec, "restart_seconds");
  *seconds = 5;
  if (delay) {
    if (delay->type != LJ_NUMBER || delay->u.number < 0 || delay->u.number > 3600 ||
        delay->u.number != (double)(int64_t)delay->u.number) return EXT_LEDGER_INVALID;
    if (delay->u.number) *seconds = (int64_t)delay->u.number;
  }
  if (*arguments && ((*arguments)->type != LJ_ARRAY || (*arguments)->u.array.len > 256)) return EXT_LEDGER_INVALID;
  if (*arguments) for (size_t i = 0; i < (*arguments)->u.array.len; i++) {
    const lj_value *arg = (*arguments)->u.array.items[i];
    if (arg->type != LJ_STRING || strlen(arg->u.string.ptr) != arg->u.string.len || !text(arg->u.string.ptr, 4096)) return EXT_LEDGER_INVALID;
  }
  if (*environment && ((*environment)->type != LJ_OBJECT || (*environment)->u.object.len > 128 || (windows && (*environment)->u.object.len)))
    return windows && *environment && (*environment)->type == LJ_OBJECT && (*environment)->u.object.len ? EXT_LEDGER_UNSUPPORTED : EXT_LEDGER_INVALID;
  if (*environment) for (size_t i = 0; i < (*environment)->u.object.len; i++) {
    const lj_member *item = &(*environment)->u.object.members[i];
    if (!environment_name(item->key.ptr) || item->value->type != LJ_STRING ||
        strlen(item->value->u.string.ptr) != item->value->u.string.len || !text(item->value->u.string.ptr, 4096)) return EXT_LEDGER_INVALID;
  }
  return 0;
}

static void render_darwin(lj_buf *b, const char *name, const char *executable, const char *workdir,
                          const char *restart, int64_t seconds, const lj_value *args, const lj_value *env) {
  lj_buf_puts(b, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<plist version=\"1.0\"><dict>\n");
  xml_pair(b, "Label", name);
  lj_buf_puts(b, "<key>ProgramArguments</key><array><string>"); xml(b, executable); lj_buf_puts(b, "</string>");
  if (args) for (size_t i = 0; i < args->u.array.len; i++) {
    lj_buf_puts(b, "<string>"); xml(b, args->u.array.items[i]->u.string.ptr); lj_buf_puts(b, "</string>");
  }
  lj_buf_puts(b, "</array>\n");
  if (*workdir) xml_pair(b, "WorkingDirectory", workdir);
  if (env && env->u.object.len) {
    lj_member **order = malloc(env->u.object.len * sizeof *order);
    if (!order) { b->oom = 1; return; }
    for (size_t i = 0; i < env->u.object.len; i++) order[i] = &env->u.object.members[i];
    qsort(order, env->u.object.len, sizeof *order, cmp_member);
    lj_buf_puts(b, "<key>EnvironmentVariables</key><dict>\n");
    for (size_t i = 0; i < env->u.object.len; i++) xml_pair(b, order[i]->key.ptr, order[i]->value->u.string.ptr);
    lj_buf_puts(b, "</dict>\n");
    free(order);
  }
  lj_buf_puts(b, "<key>RunAtLoad</key><true/>\n");
  if (!strcmp(restart, "always")) lj_buf_puts(b, "<key>KeepAlive</key><true/>\n");
  else if (!strcmp(restart, "on-failure")) lj_buf_puts(b, "<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>\n");
  else lj_buf_puts(b, "<key>KeepAlive</key><false/>\n");
  char n[32]; snprintf(n, sizeof n, "%lld", (long long)seconds);
  lj_buf_puts(b, "<key>ThrottleInterval</key><integer>"); lj_buf_puts(b, n); lj_buf_puts(b, "</integer>\n</dict></plist>\n");
}

static void render_linux(lj_buf *b, const char *description, const char *executable, const char *workdir,
                         const char *restart, int64_t seconds, const lj_value *args, const lj_value *env) {
  lj_buf_puts(b, "[Unit]\nDescription=");
  for (const char *p = description; *p; p++) {
    if (*p == '%') lj_buf_putc(b, '%');
    lj_buf_putc(b, *p == '\n' ? ' ' : *p);
  }
  lj_buf_puts(b, "\n\n[Service]\nExecStart="); systemd_quote(b, executable);
  if (args) for (size_t i = 0; i < args->u.array.len; i++) {
    lj_buf_putc(b, ' '); systemd_quote(b, args->u.array.items[i]->u.string.ptr);
  }
  lj_buf_putc(b, '\n');
  if (*workdir) { lj_buf_puts(b, "WorkingDirectory="); systemd_quote(b, workdir); lj_buf_putc(b, '\n'); }
  if (env && env->u.object.len) {
    lj_member **order = malloc(env->u.object.len * sizeof *order);
    if (!order) { b->oom = 1; return; }
    for (size_t i = 0; i < env->u.object.len; i++) order[i] = &env->u.object.members[i];
    qsort(order, env->u.object.len, sizeof *order, cmp_member);
    for (size_t i = 0; i < env->u.object.len; i++) {
      lj_buf_puts(b, "Environment=");
      lj_buf join = {0}; lj_buf_puts(&join, order[i]->key.ptr); lj_buf_putc(&join, '='); lj_buf_puts(&join, order[i]->value->u.string.ptr);
      size_t n; char *value = lj_buf_take(&join, &n);
      if (!value) b->oom = 1;
      else { systemd_quote(b, value); free(value); }
      lj_buf_putc(b, '\n');
    }
    free(order);
  }
  char n[32]; snprintf(n, sizeof n, "%lld", (long long)seconds);
  lj_buf_puts(b, "Restart="); lj_buf_puts(b, !strcmp(restart, "never") ? "no" : restart);
  lj_buf_puts(b, "\nRestartSec="); lj_buf_puts(b, n); lj_buf_puts(b, "\n\n[Install]\nWantedBy=default.target\n");
}

static void render_windows(lj_buf *b, const char *name, const char *description, const char *executable,
                           const char *workdir, const char *restart, int64_t seconds, const char *user, const lj_value *args) {
  lj_buf_puts(b, "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<Task version=\"1.3\" xmlns=\"http://schemas.microsoft.com/windows/2004/02/mit/task\">\n<RegistrationInfo><URI>\\");
  xml(b, name); lj_buf_puts(b, "</URI><Description>"); xml(b, description);
  lj_buf_puts(b, "</Description></RegistrationInfo>\n<Triggers><LogonTrigger><Enabled>true</Enabled><UserId>"); xml(b, user);
  lj_buf_puts(b, "</UserId></LogonTrigger></Triggers>\n<Principals><Principal id=\"User\"><UserId>"); xml(b, user);
  lj_buf_puts(b, "</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>\n<Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><StartWhenAvailable>true</StartWhenAvailable><ExecutionTimeLimit>PT0S</ExecutionTimeLimit>");
  if (strcmp(restart, "never")) {
    char n[32]; snprintf(n, sizeof n, "%lld", (long long)seconds);
    lj_buf_puts(b, "<RestartOnFailure><Interval>PT"); lj_buf_puts(b, n);
    lj_buf_puts(b, "S</Interval><Count>999</Count></RestartOnFailure>");
  }
  lj_buf_puts(b, "</Settings>\n<Actions Context=\"User\"><Exec><Command>"); xml(b, executable);
  lj_buf_puts(b, "</Command><Arguments>");
  if (args) for (size_t i = 0; i < args->u.array.len; i++) {
    if (i) lj_buf_putc(b, ' ');
    lj_buf quoted = {0}; windows_argument(&quoted, args->u.array.items[i]->u.string.ptr);
    size_t n; char *value = lj_buf_take(&quoted, &n);
    if (!value) b->oom = 1;
    else { xml(b, value); free(value); }
  }
  lj_buf_puts(b, "</Arguments>");
  if (*workdir) { lj_buf_puts(b, "<WorkingDirectory>"); xml(b, workdir); lj_buf_puts(b, "</WorkingDirectory>"); }
  lj_buf_puts(b, "</Exec></Actions>\n</Task>\n");
}

int ll_service_render(const ext_ledger_context *ctx, const lj_value *in, char **out, size_t *out_len) {
  int rc = ll_ctx_check(ctx);
  if (rc) return rc;
  const char *platform, *name, *description, *executable, *workdir, *restart, *user;
  const lj_value *arguments, *environment;
  int64_t seconds;
  rc = valid(in, &platform, &name, &description, &executable, &workdir, &restart, &seconds, &user, &arguments, &environment);
  if (rc) return rc;
  lj_buf content = {0}, identity = {0}, filename = {0};
  if (!strcmp(platform, "darwin")) {
    lj_buf_puts(&identity, name); lj_buf_puts(&filename, name); lj_buf_puts(&filename, ".plist");
    render_darwin(&content, name, executable, workdir, restart, seconds, arguments, environment);
  } else if (!strcmp(platform, "linux")) {
    lj_buf_puts(&identity, name);
    size_t n = strlen(name);
    if (n < 8 || strcmp(name + n - 8, ".service")) lj_buf_puts(&identity, ".service");
    lj_buf_put(&filename, identity.ptr, identity.len);
    render_linux(&content, description, executable, workdir, restart, seconds, arguments, environment);
  } else {
    lj_buf_puts(&identity, name); lj_buf_puts(&filename, name); lj_buf_puts(&filename, ".xml");
    render_windows(&content, name, description, executable, workdir, restart, seconds, user, arguments);
  }
  if (content.oom || identity.oom || filename.oom || content.len > (1 << 20)) {
    lj_buf_free(&content); lj_buf_free(&identity); lj_buf_free(&filename);
    return EXT_LEDGER_TOO_LARGE;
  }
  char digest[65]; ll_sha256_hex(content.ptr, content.len, digest);
  lj_value *result = rec_obj();
  int bad = rec_set(result, "platform", rec_str(platform)) | rec_set(result, "identity", rec_str(identity.ptr)) |
            rec_set(result, "file_name", rec_str(filename.ptr)) | rec_set(result, "content", rec_str(content.ptr)) |
            rec_set(result, "digest", rec_str(digest));
  lj_buf_free(&content); lj_buf_free(&identity); lj_buf_free(&filename);
  if (bad) { lj_free(result); return EXT_LEDGER_IO; }
  lj_buf json = {0}; lj_write(&json, result); lj_free(result);
  *out = lj_buf_take(&json, out_len);
  return *out ? 0 : EXT_LEDGER_IO;
}
