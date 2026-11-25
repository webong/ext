/* Bounded, read-only inspection of one Windows process. */
#ifdef _WIN32
#ifndef WIN32_LEAN_AND_MEAN
#define WIN32_LEAN_AND_MEAN
#endif
#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#include <processsnapshot.h>
#include <psapi.h>
#include <iphlpapi.h>
#include <tlhelp32.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include "collect.h"
#include "recorder.h"

static char *utf8(const wchar_t *w, int bytes) {
  if (!w || bytes <= 0) return NULL;
  int chars = bytes / (int)sizeof(wchar_t);
  while (chars && w[chars - 1] == 0) chars--;
  int n = WideCharToMultiByte(CP_UTF8, 0, w, chars, NULL, 0, NULL, NULL);
  if (n <= 0) return NULL;
  char *s = malloc((size_t)n + 1);
  if (!s) return NULL;
  WideCharToMultiByte(CP_UTF8, 0, w, chars, s, n, NULL, NULL);
  s[n] = 0;
  return s;
}

static int status(lj_value *p, const char *kind, DWORD error) {
  char detail[96];
  snprintf(detail, sizeof detail, "Windows error %lu", (unsigned long)error);
  if (error == ERROR_ACCESS_DENIED) return ll_process_coverage(p, kind, "permission-denied", "permission", detail);
  if (error == ERROR_NOT_SUPPORTED || error == ERROR_PROC_NOT_FOUND)
    return ll_process_coverage(p, kind, "unsupported", "unsupported", detail);
  return ll_process_coverage(p, kind, "unavailable", "no_device", detail);
}

static double seconds(FILETIME t) {
  return (double)(((uint64_t)t.dwHighDateTime << 32) | t.dwLowDateTime) / 10000000.0;
}

static void address(char out[96], int ipv6, const void *ip, DWORD port, DWORD scope) {
  char host[64] = "";
  if (!InetNtopA(ipv6 ? AF_INET6 : AF_INET, (void *)ip, host, sizeof host)) snprintf(host, sizeof host, "unknown");
  unsigned char *p = (unsigned char *)&port;
  unsigned number = (unsigned)p[0] * 256u + p[1];
  if (ipv6 && scope) snprintf(out, 96, "[%s%%%lu]:%u", host, (unsigned long)scope, number);
  else if (ipv6) snprintf(out, 96, "[%s]:%u", host, number);
  else snprintf(out, 96, "%s:%u", host, number);
}

static int usage(DWORD pid, lj_value *p) {
  HANDLE h = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, pid);
  if (!h) return status(p, "usage", GetLastError());
  FILETIME created, ended, kernel, user;
  PROCESS_MEMORY_COUNTERS_EX memory;
  memory.cb = sizeof memory;
  int times = GetProcessTimes(h, &created, &ended, &kernel, &user);
  int mem = GetProcessMemoryInfo(h, (PROCESS_MEMORY_COUNTERS *)&memory, sizeof memory);
  CloseHandle(h);
  lj_value *v = rec_obj();
  if (!v) return EXT_LEDGER_IO;
  int bad = 0;
  if (times) bad |= rec_set(v, "cpu_seconds", rec_num(seconds(kernel) + seconds(user)));
  if (mem) bad |= rec_set(v, "resident_bytes", rec_num((double)memory.WorkingSetSize));
  if (bad || rec_set(p, "usage", v)) return EXT_LEDGER_IO;
  return ll_process_coverage(p, "usage", "partial", NULL, "virtual address size and thread count are unavailable");
}

static int walk_handles(const ext_ledger_context *ctx, DWORD pid, int64_t limit, lj_value *p) {
  int rc = ll_process_coverage(p, "file", "complete", NULL, NULL) |
           ll_process_coverage(p, "ipc", "complete", NULL, NULL);
  if (rc) return rc;
  HANDLE h = OpenProcess(PROCESS_QUERY_INFORMATION | PROCESS_DUP_HANDLE, FALSE, pid);
  if (!h) {
    DWORD err = GetLastError();
    return status(p, "file", err) | status(p, "ipc", err);
  }
  HPSS snap;
  DWORD err = PssCaptureSnapshot(h, PSS_CAPTURE_HANDLES | PSS_CAPTURE_HANDLE_NAME_INFORMATION |
                                     PSS_CAPTURE_HANDLE_BASIC_INFORMATION, 0, &snap);
  if (err) {
    CloseHandle(h);
    return status(p, "file", err) | status(p, "ipc", err);
  }
  HPSSWALK marker;
  err = PssWalkMarkerCreate(NULL, &marker);
  if (err) {
    PssFreeSnapshot(GetCurrentProcess(), snap);
    CloseHandle(h);
    return status(p, "file", err) | status(p, "ipc", err);
  }
  for (int64_t count = 0; count < limit * 4; count++) {
    if ((rc = ll_ctx_check(ctx))) break;
    PSS_HANDLE_ENTRY e = {0};
    err = PssWalkSnapshot(snap, PSS_WALK_HANDLES, marker, &e, sizeof e);
    if (err == ERROR_NO_MORE_ITEMS) { err = 0; break; }
    if (err) break;
    char descriptor[32];
    snprintf(descriptor, sizeof descriptor, "%llx", (unsigned long long)(uintptr_t)e.Handle);
    char *type = utf8(e.TypeName, e.TypeNameLength);
    char *name = (e.Flags & PSS_HANDLE_HAVE_NAME) ? utf8(e.ObjectName, e.ObjectNameLength) : NULL;
    int file = type && _stricmp(type, "File") == 0 && !(name && !_strnicmp(name, "\\Device\\NamedPipe\\", 18));
    if (file) {
      rc = ll_native_resource(p, limit, "file", name, descriptor, NULL, NULL, NULL, NULL, NULL);
      if (!name && !rc) rc = ll_process_coverage(p, "file", "partial", NULL, "some file handles have no available name");
    } else {
      rc = ll_native_resource(p, limit, "ipc", NULL, descriptor, type, NULL, NULL, name, NULL);
      if (!type && !rc) rc = ll_process_coverage(p, "ipc", "partial", NULL, "some handle types are unavailable");
    }
    free(type);
    free(name);
    if (rc) break;
    if (count == limit * 4 - 1) {
      rc = ll_process_coverage(p, "file", "partial", NULL, "record limit reached") |
           ll_process_coverage(p, "ipc", "partial", NULL, "record limit reached");
    }
  }
  if (!rc && err) rc = status(p, "file", err) | status(p, "ipc", err);
  PssWalkMarkerFree(marker);
  PssFreeSnapshot(GetCurrentProcess(), snap);
  CloseHandle(h);
  return rc;
}

static int mappings(const ext_ledger_context *ctx, DWORD pid, int64_t limit, lj_value *p) {
  int rc = ll_process_coverage(p, "mapping", "complete", NULL, NULL);
  if (rc) return rc;
  HANDLE h = OpenProcess(PROCESS_QUERY_INFORMATION | PROCESS_VM_READ, FALSE, pid);
  if (!h) return status(p, "mapping", GetLastError());
  HPSS snap;
  DWORD err = PssCaptureSnapshot(h, PSS_CAPTURE_VA_SPACE | PSS_CAPTURE_VA_SPACE_SECTION_INFORMATION, 0, &snap);
  if (err) { CloseHandle(h); return status(p, "mapping", err); }
  HPSSWALK marker;
  err = PssWalkMarkerCreate(NULL, &marker);
  if (err) { PssFreeSnapshot(GetCurrentProcess(), snap); CloseHandle(h); return status(p, "mapping", err); }
  for (int64_t count = 0; count < limit * 16; count++) {
    if ((rc = ll_ctx_check(ctx))) break;
    PSS_VA_SPACE_ENTRY e = {0};
    err = PssWalkSnapshot(snap, PSS_WALK_VA_SPACE, marker, &e, sizeof e);
    if (err == ERROR_NO_MORE_ITEMS) { err = 0; break; }
    if (err) break;
    if (e.Type == MEM_MAPPED || e.Type == MEM_IMAGE) {
      char *name = utf8(e.MappedFileName, e.MappedFileNameLength);
      if (name) rc = ll_native_resource(p, limit, "mapping", name, NULL, NULL, NULL, NULL, NULL, NULL);
      else rc = ll_process_coverage(p, "mapping", "partial", NULL, "some mappings have no backing path");
      free(name);
    }
    if (rc) break;
    if (count == limit * 16 - 1) rc = ll_process_coverage(p, "mapping", "partial", NULL, "record limit reached");
  }
  if (!rc && err) rc = status(p, "mapping", err);
  PssWalkMarkerFree(marker);
  PssFreeSnapshot(GetCurrentProcess(), snap);
  CloseHandle(h);
  return rc;
}

static int sockets(const ext_ledger_context *ctx, DWORD pid, int64_t limit, lj_value *p) {
  int rc = ll_process_coverage(p, "socket", "partial", NULL, "TCP and UDP only; Unix sockets are unavailable");
  if (rc) return rc;
  for (int proto = 0; proto < 2; proto++) for (int family = 0; family < 2; family++) {
    if ((rc = ll_ctx_check(ctx))) return rc;
    ULONG af = family ? AF_INET6 : AF_INET;
    DWORD size = 0, err = proto ? GetExtendedUdpTable(NULL, &size, FALSE, af, UDP_TABLE_OWNER_PID, 0)
                            : GetExtendedTcpTable(NULL, &size, FALSE, af, TCP_TABLE_OWNER_PID_ALL, 0);
    if (err != ERROR_INSUFFICIENT_BUFFER || size > 16u << 20) {
      rc = ll_process_coverage(p, "socket", "partial", NULL, "one or more TCP/UDP tables unavailable");
      continue;
    }
    void *table = malloc(size);
    if (!table) return EXT_LEDGER_IO;
    err = proto ? GetExtendedUdpTable(table, &size, FALSE, af, UDP_TABLE_OWNER_PID, 0)
                : GetExtendedTcpTable(table, &size, FALSE, af, TCP_TABLE_OWNER_PID_ALL, 0);
    if (err) {
      free(table);
      rc = ll_process_coverage(p, "socket", "partial", NULL, "one or more TCP/UDP tables unavailable");
      continue;
    }
    DWORD count = *(DWORD *)table;
    size_t width = proto ? (family ? sizeof(MIB_UDP6ROW_OWNER_PID) : sizeof(MIB_UDPROW_OWNER_PID))
                         : (family ? sizeof(MIB_TCP6ROW_OWNER_PID) : sizeof(MIB_TCPROW_OWNER_PID));
    if (size < sizeof(DWORD) || count > (size - sizeof(DWORD)) / width) {
      free(table);
      rc = ll_process_coverage(p, "socket", "partial", NULL, "truncated TCP/UDP table");
      continue;
    }
    for (DWORD i = 0; i < count && !rc; i++) {
      DWORD owner, state = 0;
      char local[96] = "", remote[96] = "";
      if (!proto && !family) {
        MIB_TCPROW_OWNER_PID *row = &((MIB_TCPTABLE_OWNER_PID *)table)->table[i];
        owner = row->dwOwningPid; state = row->dwState;
        address(local, 0, &row->dwLocalAddr, row->dwLocalPort, 0);
        address(remote, 0, &row->dwRemoteAddr, row->dwRemotePort, 0);
      } else if (!proto) {
        MIB_TCP6ROW_OWNER_PID *row = &((MIB_TCP6TABLE_OWNER_PID *)table)->table[i];
        owner = row->dwOwningPid; state = row->dwState;
        address(local, 1, row->ucLocalAddr, row->dwLocalPort, row->dwLocalScopeId);
        address(remote, 1, row->ucRemoteAddr, row->dwRemotePort, row->dwRemoteScopeId);
      } else if (!family) {
        MIB_UDPROW_OWNER_PID *row = &((MIB_UDPTABLE_OWNER_PID *)table)->table[i];
        owner = row->dwOwningPid;
        address(local, 0, &row->dwLocalAddr, row->dwLocalPort, 0);
      } else {
        MIB_UDP6ROW_OWNER_PID *row = &((MIB_UDP6TABLE_OWNER_PID *)table)->table[i];
        owner = row->dwOwningPid;
        address(local, 1, row->ucLocalAddr, row->dwLocalPort, row->dwLocalScopeId);
      }
      if (owner != pid) continue;
      static const char *states[] = {"UNKNOWN", "CLOSED", "LISTEN", "SYN_SENT", "SYN_RECEIVED", "ESTABLISHED",
                                     "FIN_WAIT1", "FIN_WAIT2", "CLOSE_WAIT", "CLOSING", "LAST_ACK", "TIME_WAIT", "DELETE_TCB"};
      char state_number[24];
      snprintf(state_number, sizeof state_number, "%lu", (unsigned long)state);
      const char *state_name = !proto && state < sizeof states / sizeof *states ? states[state] : state_number;
      const char *protocol = proto ? (family ? "udp6" : "udp") : (family ? "tcp6" : "tcp");
      rc = ll_native_resource(p, limit, "socket", NULL, NULL, protocol, local, remote, NULL, proto ? NULL : state_name);
    }
    free(table);
  }
  return rc;
}

int ll_process_resources(const ext_ledger_context *ctx, int64_t pid, int64_t limit, lj_value *process) {
  DWORD id = (DWORD)pid;
  int rc = ll_ctx_check(ctx);
  if (!rc) rc = usage(id, process);
  if (!rc) rc = walk_handles(ctx, id, limit, process);
  if (!rc) rc = mappings(ctx, id, limit, process);
  if (!rc) rc = sockets(ctx, id, limit, process);
  return rc;
}
#endif
