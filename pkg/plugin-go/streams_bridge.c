// go:build ctx_cengine && cgo && (darwin || linux)
#include "ctx_stream.h"
extern int32_t ctxGoStreamOpen(uintptr_t,uintptr_t,uint8_t *,size_t,uintptr_t *);
extern int32_t ctxGoStreamRead(uintptr_t,uintptr_t,uint32_t,uint32_t,ctx_emit,void *);
extern int32_t ctxGoStreamClose(uintptr_t,uintptr_t);
extern void ctxGoStreamRelease(uintptr_t);
static ctx_status open_stream(void *u,const ctx_call_options *o,const ctx_cancel *life,const uint8_t *data,size_t n,void **value){
 (void)life;uintptr_t v=0;ctx_status s=ctxGoStreamOpen((uintptr_t)u,(uintptr_t)o->value,(uint8_t *)data,n,&v);*value=(void *)v;return s;
}
static ctx_status read_stream(void *u,void *v,const ctx_call_options *o,const ctx_cancel *life,uint32_t limit,ctx_emit emit,void *sink){(void)u;(void)life;return ctxGoStreamRead((uintptr_t)v,(uintptr_t)o->value,o->timeout_ms,limit,emit,sink);}
static ctx_status close_stream(void *u,void *v){return ctxGoStreamClose((uintptr_t)u,(uintptr_t)v);}
static void release_stream(void *u,void *v){(void)u;ctxGoStreamRelease((uintptr_t)v);}
ctx_status ctx_go_streams_create(uintptr_t user,uint32_t capacity,uint32_t max_age,ctx_streams **out){ctx_stream_options o={sizeof(o),capacity,max_age,(void *)user,open_stream,read_stream,close_stream,release_stream};return ctx_streams_create(&o,out);}
