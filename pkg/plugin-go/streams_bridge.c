// go:build ext_cengine && cgo && (darwin || linux)
#include "ext_stream.h"
extern int32_t ctxGoStreamOpen(uintptr_t,uintptr_t,uint8_t *,size_t,uintptr_t *);
extern int32_t ctxGoStreamRead(uintptr_t,uintptr_t,uint32_t,uint32_t,ext_emit,void *);
extern int32_t ctxGoStreamClose(uintptr_t,uintptr_t);
extern void ctxGoStreamRelease(uintptr_t);
static ext_status open_stream(void *u,const ext_call_options *o,const ext_cancel *life,const uint8_t *data,size_t n,void **value){
 (void)life;uintptr_t v=0;ext_status s=ctxGoStreamOpen((uintptr_t)u,(uintptr_t)o->value,(uint8_t *)data,n,&v);*value=(void *)v;return s;
}
static ext_status read_stream(void *u,void *v,const ext_call_options *o,const ext_cancel *life,uint32_t limit,ext_emit emit,void *sink){(void)u;(void)life;return ctxGoStreamRead((uintptr_t)v,(uintptr_t)o->value,o->timeout_ms,limit,emit,sink);}
static ext_status close_stream(void *u,void *v){return ctxGoStreamClose((uintptr_t)u,(uintptr_t)v);}
static void release_stream(void *u,void *v){(void)u;ctxGoStreamRelease((uintptr_t)v);}
ext_status ext_go_streams_create(uintptr_t user,uint32_t capacity,uint32_t max_age,ext_streams **out){ext_stream_options o={sizeof(o),capacity,max_age,(void *)user,open_stream,read_stream,close_stream,release_stream};return ext_streams_create(&o,out);}
