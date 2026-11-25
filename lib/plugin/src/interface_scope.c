#define _POSIX_C_SOURCE 200809L
/* A reviewed dependency closure for shell-command brokers. The transport never
 * supplies approval: only this in-memory object holds the private copies and
 * consumer tokens. The application maps command arguments to product policy. */
#include "interface_scope.h"
#include "services.h"
#include "fs.h"
#include "platform.h"
#include "sync.h"
#include <stdlib.h>
#include <stdio.h>
#include <string.h>
#define get yyjson_obj_get
struct ext_interface_scope {
  ext_interface_host *events;
  ext_fs *temporary;
  char *relative, *directory;
  yyjson_doc *config, *plan;
  unsigned char *needed;
  char (*tokens)[65];
  size_t count;
};
static const char *text(yyjson_val *o,const char *k) { return yyjson_get_str(get(o,k)); }
static int path_ok(yyjson_val *v) { return yyjson_is_str(v)&&yyjson_get_len(v)&&strlen(yyjson_get_str(v))==yyjson_get_len(v); }
static yyjson_val *rows(ext_interface_scope *s) { return get(yyjson_doc_get_root(s->config),"packages"); }
static yyjson_val *identity(yyjson_val *r) { return get(get(get(r,"manifest"),"descriptor"),"identity"); }
static int copy(yyjson_mut_doc *d,yyjson_mut_val *v,const char *k,yyjson_val *x) {
 yyjson_mut_val *c=yyjson_val_mut_copy(d,x); return c&&yyjson_mut_obj_add_val(d,v,k,c);
}
void ext_interface_scope_shutdown(ext_interface_scope *s) { ext_interface_host_shutdown(s->events); }
void ext_interface_scope_free(ext_interface_scope *s) {
 if(!s) return;
 if(s->events) { ext_interface_scope_shutdown(s); ext_interface_host_free(s->events); }
 if(s->temporary&&s->relative) ext_fs_remove_all(s->temporary,s->relative);
 ext_fs_close(s->temporary); free(s->relative); free(s->directory);
 yyjson_doc_free(s->config); yyjson_doc_free(s->plan);
 free(s->needed); free(s->tokens); free(s);
}
ext_plugin_status ext_interface_scope_open(yyjson_val *in,ext_interface_scope **out) {
 *out=NULL;
 if(!EXT_FIELDS(in,"object","packages","root","os","arch","temp_root")||!ext_identity(get(in,"root"))||
    !path_ok(get(in,"temp_root"))||!yyjson_is_arr(get(in,"packages"))||yyjson_arr_size(get(in,"packages"))>1024) return EXT_PLUGIN_INVALID;
 ext_interface_scope *s=calloc(1,sizeof *s); if(!s) return EXT_PLUGIN_NOMEM;
 size_t n=0; char *json=yyjson_val_write(in,0,&n);
 s->config=json?ext_parse((uint8_t*)json,n):NULL; free(json);
 s->events=ext_interface_events_new(); s->count=yyjson_arr_size(get(in,"packages"));
 s->needed=calloc(s->count,1); s->tokens=calloc(s->count,sizeof *s->tokens);
 if(!s->config||!s->events||!s->needed||!s->tokens) { ext_interface_scope_free(s); return EXT_PLUGIN_NOMEM; }
 yyjson_mut_doc *d=yyjson_mut_doc_new(NULL);
 yyjson_mut_val *v=d?yyjson_mut_obj(d):NULL,*ms=d?yyjson_mut_arr(d):NULL;
 int ok=v&&ms; if(ok) { yyjson_mut_doc_set_root(d,v); ok=yyjson_mut_obj_add_val(d,v,"packages",ms)&&copy(d,v,"os",get(in,"os"))&&copy(d,v,"arch",get(in,"arch")); }
 ext_plugin_status rc=EXT_PLUGIN_OK; size_t i,k; yyjson_val *row;
 int found=0;
 yyjson_arr_foreach(rows(s),i,k,row) {
  if(!EXT_FIELDS(row,"manifest","directory","digest")||!path_ok(get(row,"directory"))||!path_ok(get(row,"digest"))) { rc=EXT_PLUGIN_INVALID;break; }
  const char *digest=text(row,"digest");
  if(strlen(digest)!=64) { rc=EXT_PLUGIN_INVALID;break; }
  for(size_t z=0;z<64;z++) if(!((digest[z]>='0'&&digest[z]<='9')||(digest[z]>='a'&&digest[z]<='f'))) rc=EXT_PLUGIN_INVALID;
  if(ext_same_identity(identity(row),get(in,"root"))) { s->needed[i]=1;found++; }
  if(ok) ok=yyjson_mut_arr_append(ms,yyjson_val_mut_copy(d,get(row,"manifest")));
 }
 if(!rc&&found!=1) rc=EXT_PLUGIN_NOT_FOUND;
 ext_plugin_buffer plan={0};
 if(!rc&&!ok) rc=EXT_PLUGIN_NOMEM;
 /* The service takes immutable values; serialize our small request once. */
 if(!rc) {
  json=yyjson_mut_write(d,0,&n); yyjson_doc *request=json?ext_parse((uint8_t*)json,n):NULL; free(json);
  rc=request?ext_package_service("package.resolve_for",yyjson_doc_get_root(request),&plan):EXT_PLUGIN_NOMEM;
  yyjson_doc_free(request);
 }
 yyjson_mut_doc_free(d);
 if(!rc) { s->plan=ext_parse(plan.data,plan.len); if(!s->plan) rc=EXT_PLUGIN_NOMEM; }
 ext_plugin_buffer_free(&plan);
 if(rc) { ext_interface_scope_free(s);return rc; }
 yyjson_val *bindings=get(yyjson_doc_get_root(s->plan),"bindings"),*b;
 int changed=1;
 while(changed) {
  changed=0;
  yyjson_arr_foreach(bindings,i,k,b) {
   int consumer=0;
   for(size_t z=0;z<s->count;z++) if(s->needed[z]&&ext_same_identity(identity(yyjson_arr_get(rows(s),z)),get(b,"consumer"))) consumer=1;
   if(consumer) for(size_t z=0;z<s->count;z++) if(!s->needed[z]&&ext_same_identity(identity(yyjson_arr_get(rows(s),z)),get(b,"provider"))) {s->needed[z]=1;changed=1;}
  }
 }
 rc=ext_fs_open(text(in,"temp_root"),&s->temporary);
 if(!rc) rc=ext_fs_mkdtemp(s->temporary,"","ext-command-graph-",&s->relative);
 if(!rc) {
  size_t len=strlen(text(in,"temp_root"))+strlen(s->relative)+2; s->directory=malloc(len);
  if(!s->directory) rc=EXT_PLUGIN_NOMEM; else snprintf(s->directory,len,"%s/%s",text(in,"temp_root"),s->relative);
 }
 ext_plugin_store *store=NULL;
 if(!rc) rc=ext_plugin_store_open(s->directory,NULL,&store);
 yyjson_arr_foreach(rows(s),i,k,row) {
  if(rc)break;
  if(!s->needed[i])continue;
  ext_plugin_buffer result={0};
  rc=ext_plugin_store_snapshot(store,text(row,"directory"),text(identity(row),"id"),text(row,"digest"),&result);
  if(!rc) {
   yyjson_doc *actual=ext_parse(result.data,result.len);
   if(!actual||!ext_same_identity(get(get(yyjson_doc_get_root(actual),"descriptor"),"identity"),identity(row))) rc=EXT_PLUGIN_MISMATCH;
   yyjson_doc_free(actual);
  }
  ext_plugin_buffer_free(&result);
  uint8_t token[32]; if(!rc)rc=ext_random_bytes(token,sizeof token);
  if(!rc)for(size_t z=0;z<32;z++) {s->tokens[i][2*z]="0123456789abcdef"[token[z]>>4];s->tokens[i][2*z+1]="0123456789abcdef"[token[z]&15];}
 }
 ext_plugin_store_close(store);
 if(rc) {ext_interface_scope_free(s);return rc;}
 *out=s;return EXT_PLUGIN_OK;
}
ext_plugin_status ext_interface_scope_call(ext_interface_scope *s,const char *op,yyjson_val *in,char **out,size_t *len) {
 if(!strcmp(op,"events")||!strcmp(op,"answer")||!strcmp(op,"sequence")) return ext_interface_host_call(s->events,op,in,out,len);
 if(!strcmp(op,"view")) {
  yyjson_mut_doc *d=yyjson_mut_doc_new(NULL); yyjson_mut_val *v=d?yyjson_mut_obj(d):NULL,*a=d?yyjson_mut_arr(d):NULL;
  int ok=v&&a; if(ok) {yyjson_mut_doc_set_root(d,v);ok=yyjson_mut_obj_add_strcpy(d,v,"directory",s->directory)&&copy(d,v,"plan",yyjson_doc_get_root(s->plan))&&yyjson_mut_obj_add_val(d,v,"packages",a);}
  for(size_t i=0;ok&&i<s->count;i++)if(s->needed[i]) {
   yyjson_mut_val *r=yyjson_mut_obj(d);
   ok=r&&copy(d,r,"identity",identity(yyjson_arr_get(rows(s),i)))&&yyjson_mut_obj_add_strcpy(d,r,"token",s->tokens[i])&&yyjson_mut_arr_append(a,r);
  }
  *out=ok?yyjson_mut_write(d,0,len):NULL; yyjson_mut_doc_free(d);return *out?EXT_PLUGIN_OK:EXT_PLUGIN_NOMEM;
 }
 if(!strcmp(op,"dispatch")) {
  if(!EXT_FIELDS(in,"token","request")||!path_ok(get(in,"token"))||yyjson_get_len(get(in,"token"))!=64||!yyjson_is_obj(get(in,"request")))return EXT_PLUGIN_INVALID;
  const char *token=text(in,"token");size_t selected=s->count;
  for(size_t i=0;i<s->count;i++)if(s->needed[i]) {unsigned diff=0;for(size_t z=0;z<64;z++)diff|=(unsigned char)token[z]^(unsigned char)s->tokens[i][z];if(!diff)selected=i;}
  if(selected==s->count)return EXT_PLUGIN_DENIED;
  yyjson_mut_doc *d=yyjson_mut_doc_new(NULL); yyjson_mut_val *v=d?yyjson_mut_obj(d):NULL;
  int ok=v!=NULL; if(ok) {yyjson_mut_doc_set_root(d,v);ok=copy(d,v,"consumer",identity(yyjson_arr_get(rows(s),selected)))&&copy(d,v,"request",get(in,"request"));}
  size_t n=0;char *json=ok?yyjson_mut_write(d,0,&n):NULL;yyjson_mut_doc_free(d);
  if(!json)return EXT_PLUGIN_NOMEM;
  ext_plugin_status rc=n>1024*1024?EXT_PLUGIN_CAPACITY:ext_interface_events_request(s->events,"dispatch",(uint8_t*)json,n,30000,out,len);
  free(json);return rc;
 }
 return EXT_PLUGIN_UNSUPPORTED;
}
