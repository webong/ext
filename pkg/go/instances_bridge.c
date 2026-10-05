// go:build ctx_cengine && cgo && (darwin || linux)
#include "ctx_instance.h"
extern int32_t ctxGoInstanceCreate(uintptr_t,uintptr_t,uint32_t,uint8_t *,size_t,uint8_t *,size_t,uintptr_t *);
extern int32_t ctxGoInstanceDispose(uintptr_t,uintptr_t);
extern void ctxGoInstanceObserve(uintptr_t,uint8_t *,size_t,uint8_t *,size_t,char *);
/* Go performs application validation before entering C admission so its local
 * error is retained. C has already checked the JSON before this hook. */
static ctx_status validate(void *u,const uint8_t *p,size_t n){(void)u;(void)p;(void)n;return CTX_OK;}
static ctx_status create(void *u,const ctx_call_options *o,const ctx_cancel *life,const uint8_t *k,size_t kn,const uint8_t *c,size_t cn,void **value){
 (void)life;uintptr_t v=0;ctx_status s=ctxGoInstanceCreate((uintptr_t)u,(uintptr_t)o->value,o->timeout_ms,(uint8_t *)k,kn,(uint8_t *)c,cn,&v);*value=(void *)v;return s;
}
static ctx_status dispose(void *u,void *v){return ctxGoInstanceDispose((uintptr_t)u,(uintptr_t)v);}
static void observe(void *u,const uint8_t *k,size_t kn,const uint8_t *r,size_t rn,const char *s){ctxGoInstanceObserve((uintptr_t)u,(uint8_t *)k,kn,(uint8_t *)r,rn,(char *)s);}
ctx_status ctx_go_instances_create(uintptr_t user,uint32_t capacity,ctx_instances **out){ctx_instance_options o={sizeof(o),capacity,(void *)user,validate,create,dispose,observe};return ctx_instances_create(&o,out);}
uintptr_t ctx_go_lease_value(ctx_lease *l){return (uintptr_t)ctx_lease_value(l);}
