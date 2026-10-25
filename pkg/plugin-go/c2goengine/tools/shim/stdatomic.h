/* The portable core uses C11 atomics only for the cancel flag in common.c. ccgo does not translate
   the __c11_atomic builtins that <stdatomic.h> expands to, but it does translate the GCC ones. */
#ifndef C2GOENGINE_STDATOMIC_H
#define C2GOENGINE_STDATOMIC_H
typedef volatile int atomic_int;
#define atomic_init(p, v) __atomic_store_n((p), (v), __ATOMIC_SEQ_CST)
#define atomic_store(p, v) __atomic_store_n((p), (v), __ATOMIC_SEQ_CST)
#define atomic_load(p) __atomic_load_n((p), __ATOMIC_SEQ_CST)
#endif
