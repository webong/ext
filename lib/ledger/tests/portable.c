/* Unsupported native operations must fail explicitly in the translated profile. */
#include "ext_ledger.h"
#include <stdio.h>
#include <string.h>
int main(void) {
  char *out=NULL;size_t len=0;uint64_t store=0,recorder=0;
  const char *config="{\"object\":\"store\",\"backend\":\"memory\"}";
  if(ext_ledger_open(config,strlen(config),&store))return 1;
  char input[256];
  snprintf(input,sizeof input,"{\"object\":\"recorder\",\"machine\":true,\"store\":%llu}",(unsigned long long)store);
  if(ext_ledger_open(input,strlen(input),&recorder)!=EXT_LEDGER_UNSUPPORTED)return 2;
  if(ext_ledger_collect_environment(&out,&len)!=EXT_LEDGER_UNSUPPORTED)return 3;
  if(ext_ledger_collect(NULL,"processes",&out,&len)!=EXT_LEDGER_UNSUPPORTED)return 4;
  if(ext_ledger_close(store))return 5;
  puts("portable profile checks passed");return 0;
}
