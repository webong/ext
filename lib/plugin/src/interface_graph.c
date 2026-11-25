#define _POSIX_C_SOURCE 200809L
/* C-owned execution lifetime. Applications supply reviewed digests and answer
 * reverse policy events; neither those facts nor a plan are persistent grants. */
#include "interface_graph.h"
#include "interface_store.h"
#include "fs.h"
#include "services.h"
#include "sync.h"
#include "progress.h"
#include "platform.h"
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#define get yyjson_obj_get
struct ext_interface_graph {
  pthread_mutex_t mu, shutdown_mu;
  int starting, ready, closing;
  char instance[49];
  yyjson_doc *config, *plan;
  ext_interface_host *events;
  uint64_t *hosts;
  size_t count, root;
  ext_fs *temporary;
  char *relative, *directory;
};
static const char *text(yyjson_val *v, const char *key) { return yyjson_get_str(get(v, key)); }
static int copy(yyjson_mut_doc *d, yyjson_mut_val *v, const char *key, yyjson_val *value) {
  yyjson_mut_val *c = yyjson_val_mut_copy(d, value);
  return c && yyjson_mut_obj_add_val(d, v, key, c);
}
static int path_ok(yyjson_val *v) {
  return yyjson_is_str(v) && yyjson_get_len(v) && strlen(yyjson_get_str(v)) == yyjson_get_len(v);
}
static ext_plugin_status emit(yyjson_val *v, char **out, size_t *len) {
  *out = yyjson_val_write(v, 0, len);
  return *out ? EXT_PLUGIN_OK : EXT_PLUGIN_NOMEM;
}
static ext_plugin_status call_doc(uint64_t handle, const char *op, yyjson_mut_doc *d, yyjson_doc **answer) {
  size_t len = 0, result_len = 0;
  char *json = yyjson_mut_write(d, 0, &len), *result = NULL;
  if (!json) return EXT_PLUGIN_NOMEM;
  ext_plugin_status s = ext_plugin_call(handle, op, json, len, &result, &result_len);
  free(json);
  if (!s && answer) {
    *answer = ext_parse((uint8_t *)result, result_len);
    if (!*answer) s = EXT_PLUGIN_NOMEM;
  }
  ext_plugin_free(result);
  return s;
}
static yyjson_val *rows(ext_interface_graph *g) { return get(yyjson_doc_get_root(g->config), "packages"); }
static yyjson_val *nodes(ext_interface_graph *g) { return get(yyjson_doc_get_root(g->plan), "nodes"); }
static yyjson_val *row_for(ext_interface_graph *g, yyjson_val *identity) {
  size_t i,n; yyjson_val *row;
  yyjson_arr_foreach(rows(g), i,n,row)
    if (ext_same_identity(get(get(get(row,"manifest"),"descriptor"),"identity"), identity)) return row;
  return NULL;
}
static int canceled(ext_interface_graph *g) {
  pthread_mutex_lock(&g->mu); int closed = g->closing; pthread_mutex_unlock(&g->mu); return closed;
}
static void cleanup(ext_interface_graph *g) {
  if (g->temporary && g->relative) ext_fs_remove_all(g->temporary, g->relative);
  free(g->relative); g->relative = NULL;
  free(g->directory); g->directory = NULL;
  ext_fs_close(g->temporary); g->temporary = NULL;
}
void ext_interface_graph_shutdown(ext_interface_graph *g) {
  pthread_mutex_lock(&g->mu); g->closing = 1; pthread_mutex_unlock(&g->mu);
  pthread_mutex_lock(&g->shutdown_mu);
  ext_interface_host_shutdown(g->events); /* wake denied/unanswered policy calls first */
  yyjson_val *order = get(yyjson_doc_get_root(g->plan), "order");
  for (size_t i = yyjson_arr_size(order); i > 0; i--) {
    int64_t index = 0;
    if (!ext_int64(yyjson_arr_get(order, i-1), &index) || index < 0 || (size_t)index >= g->count) continue;
    pthread_mutex_lock(&g->mu);
    uint64_t handle = g->hosts[index]; g->hosts[index] = 0;
    pthread_mutex_unlock(&g->mu);
    if (handle) ext_plugin_close(handle);
  }
  pthread_mutex_unlock(&g->shutdown_mu);
}
void ext_interface_graph_free(ext_interface_graph *g) {
  if (!g) return;
  ext_interface_graph_shutdown(g);
  cleanup(g); /* table references ensure no start/call still uses the copies */
  ext_interface_host_free(g->events);
  yyjson_doc_free(g->config); yyjson_doc_free(g->plan);
  free(g->hosts); pthread_mutex_destroy(&g->shutdown_mu); pthread_mutex_destroy(&g->mu); free(g);
}
ext_plugin_status ext_interface_graph_open(yyjson_val *config, ext_interface_graph **out) {
  *out = NULL;
  ext_plugin_store_layout layout;
  if (!EXT_FIELDS(config,"object","packages","root","os","arch","max_hosts","temp_root","layout") ||
      ext_interface_store_layout(config,&layout) ||
      !path_ok(get(config,"temp_root")) || !yyjson_is_arr(get(config,"packages"))) return EXT_PLUGIN_INVALID;
  yyjson_mut_doc *d = yyjson_mut_doc_new(NULL);
  yyjson_mut_val *v = d ? yyjson_mut_obj(d) : NULL, *manifests = d ? yyjson_mut_arr(d) : NULL;
  int ok = v && manifests;
  if (ok) { yyjson_mut_doc_set_root(d,v); ok = copy(d,v,"root",get(config,"root")) &&
    copy(d,v,"os",get(config,"os")) && copy(d,v,"arch",get(config,"arch")) &&
    yyjson_mut_obj_add_val(d,v,"packages",manifests); }
  if (ok && get(config,"max_hosts")) ok = copy(d,v,"max_hosts",get(config,"max_hosts"));
  size_t i,n; yyjson_val *row;
  ext_plugin_status s = EXT_PLUGIN_OK;
  yyjson_arr_foreach(get(config,"packages"),i,n,row) {
    if (!EXT_FIELDS(row,"manifest","directory") || !path_ok(get(row,"directory"))) { s = EXT_PLUGIN_INVALID; break; }
    if (ok) ok = yyjson_mut_arr_append(manifests, yyjson_val_mut_copy(d,get(row,"manifest")));
  }
  yyjson_doc *plan = NULL;
  if (!s) s = ok ? call_doc(0,"package.execution",d,&plan) : EXT_PLUGIN_NOMEM;
  yyjson_mut_doc_free(d);
  if (s) return s;
  size_t len=0; char *bytes=yyjson_val_write(config,0,&len);
  yyjson_doc *owned=bytes ? ext_parse((uint8_t *)bytes,len) : NULL; free(bytes);
  ext_interface_graph *g=calloc(1,sizeof *g);
  if (!g || !owned) { free(g); yyjson_doc_free(owned); yyjson_doc_free(plan); return EXT_PLUGIN_NOMEM; }
  pthread_mutex_init(&g->mu,NULL); pthread_mutex_init(&g->shutdown_mu,NULL);
  g->config=owned; g->plan=plan; g->count=yyjson_arr_size(nodes(g));
  int64_t root=0; ext_int64(get(yyjson_doc_get_root(plan),"root"),&root); g->root=(size_t)root;
  g->hosts=calloc(g->count,sizeof *g->hosts); g->events=ext_interface_events_new();
  if (!g->hosts || !g->events) {
    free(g->hosts); if(g->events) ext_interface_host_free(g->events);
    yyjson_doc_free(owned); yyjson_doc_free(plan); pthread_mutex_destroy(&g->shutdown_mu); pthread_mutex_destroy(&g->mu); free(g); return EXT_PLUGIN_NOMEM;
  }
  uint8_t random[24];
  s=ext_random_bytes(random,sizeof random);
  if(s) { ext_interface_graph_free(g); return s; }
  for(size_t z=0;z<sizeof random;z++) {
    g->instance[2*z]="0123456789abcdef"[random[z]>>4];
    g->instance[2*z+1]="0123456789abcdef"[random[z]&15];
  }
  *out=g; return EXT_PLUGIN_OK;
}
static const char *review_for(yyjson_val *reviews, yyjson_val *identity) {
  size_t i,n; yyjson_val *r;
  yyjson_arr_foreach(reviews,i,n,r) if(ext_same_identity(get(r,"identity"),identity)) return text(r,"digest");
  return NULL;
}
static ext_plugin_status entry(ext_interface_graph *g, ext_plugin_store *store, yyjson_val *node,
                               const char *digest, char **path) {
  yyjson_val *config=yyjson_doc_get_root(g->config), *contract=get(node,"contract");
  ext_plugin_buffer b={0};
  ext_plugin_status s=ext_plugin_store_resolve_entrypoint(store,text(get(node,"identity"),"id"),
    text(contract,"name"),text(contract,"version"),"ext.plugin/v1",digest,text(config,"os"),text(config,"arch"),&b);
  if (!s) {
    yyjson_doc *doc=ext_parse(b.data,b.len);
    const char *p=doc ? text(yyjson_doc_get_root(doc),"path") : NULL;
    *path=p ? strdup(p) : NULL;
    if(!*path) s=EXT_PLUGIN_NOMEM;
    yyjson_doc_free(doc);
  }
  ext_plugin_buffer_free(&b); return s;
}
static ext_plugin_status start_graph(ext_interface_graph *g, yyjson_val *in) {
  yyjson_val *reviews=get(in,"reviews"), *config=yyjson_doc_get_root(g->config);
  int64_t timeout=5000;
  if (!EXT_FIELDS(in,"reviews","timeout_ms") || !yyjson_is_arr(reviews) || yyjson_arr_size(reviews)>1024 ||
      (get(in,"timeout_ms") && (!ext_int64(get(in,"timeout_ms"),&timeout) || timeout<1 || timeout>UINT32_MAX))) return EXT_PLUGIN_INVALID;
  size_t i,n,j,k; yyjson_val *r,*other;
  yyjson_arr_foreach(reviews,i,n,r) {
    const char *digest=text(r,"digest");
    if(!EXT_FIELDS(r,"identity","digest") || !ext_identity(get(r,"identity")) || !digest || yyjson_get_len(get(r,"digest"))!=64) return EXT_PLUGIN_INVALID;
    for(size_t z=0;z<64;z++) if(!((digest[z]>='0'&&digest[z]<='9')||(digest[z]>='a'&&digest[z]<='f'))) return EXT_PLUGIN_INVALID;
    EXT_ARR_BEFORE(reviews,i,j,k,other) if(ext_same_identity(get(r,"identity"),get(other,"identity"))) return EXT_PLUGIN_INVALID;
  }
  pthread_mutex_lock(&g->mu);
  if(g->closing || g->starting) { pthread_mutex_unlock(&g->mu); return EXT_PLUGIN_CLOSED; }
  g->starting=1; pthread_mutex_unlock(&g->mu);
  int64_t deadline=ext_mono_ns()/1000000+timeout;
  ext_plugin_status s=EXT_PLUGIN_OK;
  yyjson_val *node;
  /* Every reachable identity needs explicit application review before any copy. */
  yyjson_arr_foreach(nodes(g),i,n,node) if(!review_for(reviews,get(node,"identity"))) { s=EXT_PLUGIN_DENIED; break; }
  if(!s) s=ext_fs_open(text(config,"temp_root"),&g->temporary);
  if(!s) s=ext_fs_mkdtemp(g->temporary,"","ext-graph-snapshot-",&g->relative);
  if(!s) {
    size_t len=strlen(text(config,"temp_root"))+strlen(g->relative)+2;
    g->directory=malloc(len);
    if(!g->directory) s=EXT_PLUGIN_NOMEM;
    else snprintf(g->directory,len,"%s/%s",text(config,"temp_root"),g->relative);
  }
  ext_plugin_store *snapshot=NULL;
  ext_plugin_store_layout layout;
  if(!s) s=ext_interface_store_layout(config,&layout);
  if(!s) s=ext_plugin_store_open_with_layout(g->directory,NULL,layout.manifest_path?&layout:NULL,&snapshot);
  yyjson_arr_foreach(nodes(g),i,n,node) {
    if(s) break;
    if(canceled(g)) { s=EXT_PLUGIN_CANCELED; break; }
    if(ext_mono_ns()/1000000>=deadline) { s=EXT_PLUGIN_TIMEOUT; break; }
    int prior=0;
    for(size_t z=0;z<i;z++) if(ext_same_identity(get(yyjson_arr_get(nodes(g),z),"identity"),get(node,"identity"))) prior=1;
    if(prior) continue;
    yyjson_val *row=row_for(g,get(node,"identity"));
    ext_plugin_buffer out={0};
    s=ext_plugin_store_snapshot(snapshot,text(row,"directory"),text(get(node,"identity"),"id"),review_for(reviews,get(node,"identity")),&out);
    if (!s) {
      yyjson_doc *actual=ext_parse(out.data,out.len);
      if (!actual || !yyjson_equals(yyjson_doc_get_root(actual),get(row,"manifest"))) s=EXT_PLUGIN_MISMATCH;
      yyjson_doc_free(actual);
    }
    ext_plugin_buffer_free(&out);
  }
  char **paths=calloc(g->count,sizeof *paths);
  if(!paths && !s) s=EXT_PLUGIN_NOMEM;
  yyjson_arr_foreach(nodes(g),i,n,node) {
    if(s) break;
    s=entry(g,snapshot,node,review_for(reviews,get(node,"identity")),&paths[i]);
  }
  ext_plugin_store_close(snapshot);
  yyjson_val *order=get(yyjson_doc_get_root(g->plan),"order"), *position;
  yyjson_arr_foreach(order,i,n,position) {
    if(s) break;
    if(canceled(g)) { s=EXT_PLUGIN_CANCELED; break; }
    int64_t remaining=deadline-ext_mono_ns()/1000000, index=0;
    if(remaining<=0) { s=EXT_PLUGIN_TIMEOUT; break; }
    ext_int64(position,&index); node=yyjson_arr_get(nodes(g),(size_t)index);
    yyjson_val *row=row_for(g,get(node,"identity"));
    yyjson_mut_doc *d=yyjson_mut_doc_new(NULL);
    yyjson_mut_val *v=d?yyjson_mut_obj(d):NULL, *backend=d?yyjson_mut_obj(d):NULL, *routes=d?yyjson_mut_arr(d):NULL;
    int ok=v&&backend&&routes;
    if(ok) { yyjson_mut_doc_set_root(d,v); ok=yyjson_mut_obj_add_str(d,v,"object","host") &&
      copy(d,v,"descriptor",get(get(row,"manifest"),"descriptor")) && yyjson_mut_obj_add_str(d,v,"verify","allow") &&
      yyjson_mut_obj_add_str(d,v,"authorize","allow") && yyjson_mut_obj_add_val(d,v,"backend",backend) &&
      yyjson_mut_obj_add_str(d,backend,"kind","process") && yyjson_mut_obj_add_strcpy(d,backend,"executable",paths[index]); }
    yyjson_val *route; size_t a,b;
    yyjson_arr_foreach(get(node,"routes"),a,b,route) {
      uint64_t target=0;
      for(size_t z=0;z<g->count;z++) {
        yyjson_val *candidate=yyjson_arr_get(nodes(g),z);
        if(ext_same_identity(get(candidate,"identity"),get(route,"provider")) && ext_same_ref(get(candidate,"contract"),get(route,"contract"))) {
          pthread_mutex_lock(&g->mu); target=g->hosts[z]; pthread_mutex_unlock(&g->mu); break;
        }
      }
      yyjson_mut_val *edge=d?yyjson_mut_obj(d):NULL;
      if(ok) ok=target&&edge&&copy(d,edge,"contract",get(route,"contract"))&&copy(d,edge,"operation",get(route,"operation"))&&
        yyjson_mut_obj_add_uint(d,edge,"target",target)&&yyjson_mut_arr_append(routes,edge);
    }
    if(ok && yyjson_arr_size(get(node,"routes"))) ok=yyjson_mut_obj_add_val(d,v,"routes",routes);
    size_t len=0; char *json=ok?yyjson_mut_write(d,0,&len):NULL; uint64_t host=0;
    s=json?ext_plugin_open(json,len,&host):EXT_PLUGIN_NOMEM;
    free(json); yyjson_mut_doc_free(d);
    if(s) break;
    s=ext_interface_host_route_events(host,g->events,get(node,"identity"),get(node,"routes"));
    pthread_mutex_lock(&g->mu);
    if(!s && !g->closing) g->hosts[index]=host;
    else if(!s) s=EXT_PLUGIN_CANCELED;
    pthread_mutex_unlock(&g->mu);
    if(s) { ext_plugin_close(host); break; }
    char input[64]; snprintf(input,sizeof input,"{\"timeout_ms\":%lld}",(long long)remaining);
    char *output=NULL; size_t output_len=0;
    s=ext_plugin_call(host,"start",input,strlen(input),&output,&output_len); ext_plugin_free(output);
  }
  if(paths) { for(size_t z=0;z<g->count;z++) free(paths[z]); free(paths); }
  pthread_mutex_lock(&g->mu);
  if(!s && g->closing) s=EXT_PLUGIN_CANCELED;
  if(!s) g->ready=1;
  pthread_mutex_unlock(&g->mu);
  if(s) { ext_interface_graph_shutdown(g); cleanup(g); }
  return s;
}
/* The caller correlates a live observation with ledger's process identities.
 * Channel ownership prevents reaping/PID reuse until graph shutdown. Recheck
 * every live session before publishing; a failed/closing graph yields no partial
 * desired namespace that could accidentally erase another observation. */
static void session_id(ext_interface_graph *g,size_t i,char out[80]) {
  /* Execution plans cap hosts at 1024; keep the formatted index bounded too. */
  snprintf(out,80,"plugin-session/%s/%u",g->instance,(unsigned)i);
}
static ext_plugin_status observe(ext_interface_graph *g,yyjson_doc **out) {
  yyjson_mut_doc *d=yyjson_mut_doc_new(NULL);
  yyjson_mut_val *a=d?yyjson_mut_arr(d):NULL;
  if(!a) { yyjson_mut_doc_free(d); return EXT_PLUGIN_NOMEM; }
  yyjson_mut_doc_set_root(d,a);
  ext_plugin_status s=EXT_PLUGIN_OK;
  pthread_mutex_lock(&g->mu);
  if(!g->ready || g->closing) s=EXT_PLUGIN_CLOSED;
  for(size_t i=0;!s && i<g->count;i++) {
    char *json=NULL; size_t len=0;
    s=ext_plugin_call(g->hosts[i],"process_id","{}",2,&json,&len);
    yyjson_doc *pid=!s?ext_parse((uint8_t *)json,len):NULL;
    ext_plugin_free(json);
    if(!s && !pid) s=EXT_PLUGIN_NOMEM;
    yyjson_mut_val *v=!s?yyjson_val_mut_copy(d,yyjson_arr_get(nodes(g),i)):NULL;
    char id[80]; session_id(g,i,id);
    if(!s && (!v || !yyjson_mut_obj_add_strcpy(d,v,"session",id) ||
       !copy(d,v,"pid",yyjson_doc_get_root(pid)) || !yyjson_mut_arr_append(a,v))) s=EXT_PLUGIN_NOMEM;
    yyjson_doc_free(pid);
  }
  pthread_mutex_unlock(&g->mu);
  if(!s) {
    size_t len=0; char *json=yyjson_mut_write(d,0,&len);
    *out=json?ext_parse((uint8_t *)json,len):NULL; free(json);
    if(!*out) s=EXT_PLUGIN_NOMEM;
  }
  yyjson_mut_doc_free(d); return s;
}
static int vertex(yyjson_mut_doc *d,yyjson_mut_val *a,const char *id,const char *kind,yyjson_mut_val *attrs) {
  yyjson_mut_val *v=yyjson_mut_obj(d);
  return v && yyjson_mut_obj_add_strcpy(d,v,"id",id) && yyjson_mut_obj_add_strcpy(d,v,"kind",kind) &&
    yyjson_mut_obj_add_val(d,v,"attributes",attrs) && yyjson_mut_arr_append(a,v);
}
static int edge(yyjson_mut_doc *d,yyjson_mut_val *a,const char *id,const char *type,const char *from,const char *to) {
  yyjson_mut_val *v=yyjson_mut_obj(d);
  return v && yyjson_mut_obj_add_strcpy(d,v,"id",id) && yyjson_mut_obj_add_strcpy(d,v,"type",type) &&
    yyjson_mut_obj_add_strcpy(d,v,"from",from) && yyjson_mut_obj_add_strcpy(d,v,"to",to) && yyjson_mut_arr_append(a,v);
}
static ext_plugin_status graph_snapshot(ext_interface_graph *g,yyjson_val *in,char **out,size_t *len) {
  yyjson_val *refs=get(in,"processes");
  if(!EXT_FIELDS(in,"namespace","processes") || !path_ok(get(in,"namespace")) ||
     yyjson_get_len(get(in,"namespace"))>128 || !yyjson_is_arr(refs) || yyjson_arr_size(refs)!=g->count) return EXT_PLUGIN_INVALID;
  const char *ns=text(in,"namespace");
  for(size_t z=0;ns[z];z++) {
    int alnum=(ns[z]>='a'&&ns[z]<='z')||(ns[z]>='A'&&ns[z]<='Z')||(ns[z]>='0'&&ns[z]<='9');
    if((!z&&!alnum)||(!alnum&&ns[z]!='.'&&ns[z]!='_'&&ns[z]!='-')) return EXT_PLUGIN_INVALID;
  }
  char sk[160],pk[160],rk[160],ek[160],package_kind[160],package_edge[160];
  snprintf(sk,sizeof sk,"%s/session",ns); snprintf(pk,sizeof pk,"%s/process-reference",ns);
  snprintf(rk,sizeof rk,"%s/routes-to",ns); snprintf(ek,sizeof ek,"%s/runs-in",ns);
  snprintf(package_kind,sizeof package_kind,"%s/package-reference",ns);
  snprintf(package_edge,sizeof package_edge,"%s/instance-of",ns);
  yyjson_doc *observation=NULL;
  ext_plugin_status s=observe(g,&observation);
  if(s) return s;
  yyjson_val *live=yyjson_doc_get_root(observation);
  /* References are caller-supplied observations, never authority. Require a
   * complete one-to-one correlation, including the original session and PID. */
  for(size_t i=0;i<g->count;i++) {
    yyjson_val *r=yyjson_arr_get(refs,i), *node=yyjson_arr_get(live,i);
    int64_t pid=0,actual=0;
    if(!EXT_FIELDS(r,"session","namespace","id","host_id","pid","start_id") ||
       !path_ok(get(r,"session")) || strcmp(text(r,"session"),text(node,"session")) ||
       !path_ok(get(r,"namespace")) || strcmp(text(r,"namespace"),"ext.system") ||
       !path_ok(get(r,"id")) || strncmp(text(r,"id"),"host-process/",13) || yyjson_get_len(get(r,"id"))!=37 ||
       !path_ok(get(r,"host_id")) || !path_ok(get(r,"start_id")) ||
       !ext_int64(get(r,"pid"),&pid) || !ext_int64(get(node,"pid"),&actual) || pid!=actual) { s=EXT_PLUGIN_MISMATCH; break; }
    for(size_t z=13;z<37;z++) {
      char c=text(r,"id")[z];
      if(!((c>='0'&&c<='9')||(c>='a'&&c<='f'))) s=EXT_PLUGIN_MISMATCH;
    }
    for(size_t z=0;z<i;z++) if(!strcmp(text(yyjson_arr_get(refs,z),"id"),text(r,"id"))) s=EXT_PLUGIN_MISMATCH;
    if(s) break;
  }
  yyjson_mut_doc *d=!s?yyjson_mut_doc_new(NULL):NULL;
  yyjson_mut_val *v=d?yyjson_mut_obj(d):NULL,*schema=d?yyjson_mut_obj(d):NULL,*tx=d?yyjson_mut_obj(d):NULL;
  yyjson_mut_val *vs=d?yyjson_mut_arr(d):NULL,*es=d?yyjson_mut_arr(d):NULL;
  int ok=v&&schema&&tx&&vs&&es;
  if(ok) {
    yyjson_mut_doc_set_root(d,v);
    ok=yyjson_mut_obj_add_val(d,v,"schema",schema)&&yyjson_mut_obj_add_val(d,v,"transaction",tx)&&
      copy(d,schema,"namespace",get(in,"namespace"))&&yyjson_mut_obj_add_str(d,schema,"version","1")&&
      copy(d,tx,"namespace",get(in,"namespace"))&&yyjson_mut_obj_add_val(d,tx,"vertices",vs)&&yyjson_mut_obj_add_val(d,tx,"edges",es);
  }
  if(ok) {
    yyjson_mut_val *kinds=yyjson_mut_arr(d),*rels=yyjson_mut_arr(d);
    ok=kinds&&rels&&yyjson_mut_obj_add_val(d,schema,"kinds",kinds)&&yyjson_mut_obj_add_val(d,schema,"relationships",rels)&&
      yyjson_mut_arr_add_strcpy(d,kinds,sk)&&yyjson_mut_arr_add_strcpy(d,kinds,pk)&&yyjson_mut_arr_add_strcpy(d,kinds,package_kind);
    const char *types[]={rk,ek,package_edge},*targets[]={sk,pk,package_kind};
    for(size_t z=0;ok&&z<3;z++) {
      yyjson_mut_val *r=yyjson_mut_obj(d);
      ok=r&&yyjson_mut_obj_add_strcpy(d,r,"type",types[z])&&yyjson_mut_obj_add_strcpy(d,r,"from",sk)&&
        yyjson_mut_obj_add_strcpy(d,r,"to",targets[z])&&yyjson_mut_arr_append(rels,r);
    }
  }
  for(size_t i=0;ok && i<g->count;i++) {
    yyjson_val *node=yyjson_arr_get(live,i),*ref=yyjson_arr_get(refs,i);
    const char *sid=text(node,"session"),*pid=text(ref,"id");
    yyjson_mut_val *attrs=yyjson_mut_obj(d),*pa=yyjson_mut_obj(d);
    char eid[128]; snprintf(eid,sizeof eid,"%s/process",sid);
    ok=attrs&&pa&&copy(d,attrs,"identity",get(node,"identity"))&&copy(d,attrs,"contract",get(node,"contract"))&&
      vertex(d,vs,sid,sk,attrs)&&copy(d,pa,"reference",ref)&&
      vertex(d,vs,pid,pk,pa)&&edge(d,es,eid,ek,sid,pid);
    char package_id[65]; ext_package_graph_identity(get(node,"identity"),package_id);
    yyjson_mut_val *package_attrs=yyjson_mut_obj(d),*reference=yyjson_mut_obj(d);
    snprintf(eid,sizeof eid,"%s/package",sid);
    int first=1;
    for(size_t z=0;z<i;z++) if(ext_same_identity(get(yyjson_arr_get(live,z),"identity"),get(node,"identity"))) first=0;
    if(ok && first) ok=package_attrs&&reference&&copy(d,package_attrs,"identity",get(node,"identity"))&&
      yyjson_mut_obj_add_str(d,reference,"namespace","ext.plugin")&&yyjson_mut_obj_add_strcpy(d,reference,"id",package_id)&&
      yyjson_mut_obj_add_val(d,package_attrs,"reference",reference)&&vertex(d,vs,package_id,package_kind,package_attrs);
    if(ok) ok=edge(d,es,eid,package_edge,sid,package_id);
    size_t a,b; yyjson_val *route;
    yyjson_arr_foreach(get(node,"routes"),a,b,route) {
      for(size_t j=0;ok && j<g->count;j++) {
        yyjson_val *provider=yyjson_arr_get(live,j);
        if(ext_same_identity(get(provider,"identity"),get(route,"provider")) && ext_same_ref(get(provider,"contract"),get(route,"contract"))) {
          snprintf(eid,sizeof eid,"%s/route/%zu",sid,a);
          ok=edge(d,es,eid,rk,sid,text(provider,"session"));
          if(ok) ok=copy(d,yyjson_mut_arr_get(es,yyjson_mut_arr_size(es)-1),"attributes",route);
          break;
        }
      }
    }
  }
  if(!s) s=ok?EXT_PLUGIN_OK:EXT_PLUGIN_NOMEM;
  /* Reject close already observed here. This is a dated fact, not a lease:
   * the owner serializes publication/reconciliation with its shutdown. */
  if(!s && canceled(g)) s=EXT_PLUGIN_CLOSED;
  if(!s) { *out=yyjson_mut_write(d,0,len); if(!*out) s=EXT_PLUGIN_NOMEM; }
  yyjson_mut_doc_free(d); yyjson_doc_free(observation); return s;
}
ext_plugin_status ext_interface_graph_call(ext_interface_graph *g,const char *op,yyjson_val *in,char **out,size_t *len) {
  if(!strcmp(op,"events") || !strcmp(op,"answer") || !strcmp(op,"sequence"))
    return ext_interface_host_call(g->events,op,in,out,len);
  if(!strcmp(op,"observe")) {
    if(!yyjson_is_obj(in) || yyjson_obj_size(in)) return EXT_PLUGIN_INVALID;
    yyjson_doc *observation=NULL;
    ext_plugin_status s=observe(g,&observation);
    if(!s) s=emit(yyjson_doc_get_root(observation),out,len);
    yyjson_doc_free(observation); return s;
  }
  if(!strcmp(op,"snapshot")) return graph_snapshot(g,in,out,len);
  if(!strcmp(op,"plan")) return emit(yyjson_doc_get_root(g->plan),out,len);
  if(!strcmp(op,"start")) {
    ext_plugin_status s=start_graph(g,in);
    if(!s) { *out=strdup("null"); *len=4; if(!*out) s=EXT_PLUGIN_NOMEM; }
    return s;
  }
  if(strcmp(op,"call")) return EXT_PLUGIN_UNSUPPORTED;
  if(!EXT_FIELDS(in,"call","timeout_ms")) return EXT_PLUGIN_INVALID;
  pthread_mutex_lock(&g->mu);
  uint64_t root=g->ready&&!g->closing?g->hosts[g->root]:0;
  pthread_mutex_unlock(&g->mu);
  if(!root) return EXT_PLUGIN_CLOSED;
  size_t n=0; char *json=yyjson_val_write(in,0,&n);
  if(!json) return EXT_PLUGIN_NOMEM;
  ext_plugin_status s=ext_plugin_call(root,"call",json,n,out,len); free(json); return s;
}
