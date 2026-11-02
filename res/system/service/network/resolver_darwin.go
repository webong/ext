//go:build darwin && cgo

package network

/*
#cgo LDFLAGS: -framework CoreFoundation -framework SystemConfiguration
#include <CoreFoundation/CoreFoundation.h>
#include <SystemConfiguration/SystemConfiguration.h>
#include <stdlib.h>
#include <string.h>

static void appendDNS(CFMutableStringRef output, CFDictionaryRef dns, CFStringRef key, CFStringRef prefix) {
    CFTypeRef raw = CFDictionaryGetValue(dns, key);
    if (!raw || CFGetTypeID(raw) != CFArrayGetTypeID()) return;
    CFArrayRef values = (CFArrayRef)raw;
    CFIndex count = CFArrayGetCount(values);
    if (count > 256) count = 256;
    for (CFIndex i = 0; i < count; i++) {
        CFTypeRef item = CFArrayGetValueAtIndex(values, i);
        char text[256];
        if (!item || CFGetTypeID(item) != CFStringGetTypeID() ||
            !CFStringGetCString((CFStringRef)item, text, sizeof(text), kCFStringEncodingUTF8) ||
            strchr(text, '\n') || strchr(text, '\r')) continue;
        CFStringAppend(output, prefix);
        CFStringAppend(output, (CFStringRef)item);
        CFStringAppend(output, CFSTR("\n"));
    }
}
static char *copyDNSState(void) {
    SCDynamicStoreRef store = SCDynamicStoreCreate(NULL, CFSTR("ExtNetwork"), NULL, NULL);
    if (!store) return NULL;
    const void *key = CFSTR("State:/Network/Global/DNS");
    const void *pattern = CFSTR("State:/Network/Service/[^/]+/DNS");
    CFArrayRef keys = CFArrayCreate(NULL, &key, 1, &kCFTypeArrayCallBacks);
    CFArrayRef patterns = CFArrayCreate(NULL, &pattern, 1, &kCFTypeArrayCallBacks);
    CFDictionaryRef all = SCDynamicStoreCopyMultiple(store, keys, patterns);
    CFRelease(keys); CFRelease(patterns); CFRelease(store);
    if (!all) return NULL;
    CFIndex count = CFDictionaryGetCount(all);
    if (count > 256) { CFRelease(all); return NULL; }
    const void *values[256];
    CFDictionaryGetKeysAndValues(all, NULL, values);
    CFMutableStringRef output = CFStringCreateMutable(NULL, 0);
    for (CFIndex i = 0; i < count; i++) {
        if (CFGetTypeID(values[i]) != CFDictionaryGetTypeID()) continue;
        CFDictionaryRef dns = (CFDictionaryRef)values[i];
        appendDNS(output, dns, CFSTR("ServerAddresses"), CFSTR("S\t"));
        appendDNS(output, dns, CFSTR("SearchDomains"), CFSTR("D\t"));
    }
    CFRelease(all);
    CFIndex size = CFStringGetMaximumSizeForEncoding(CFStringGetLength(output), kCFStringEncodingUTF8) + 1;
    char *result = malloc((size_t)size);
    if (result && !CFStringGetCString(output, result, size, kCFStringEncodingUTF8)) { free(result); result = NULL; }
    CFRelease(output);
    return result;
}
*/
import "C"

import (
	"context"
	"net"
	"strings"
	"unsafe"
)

func platformResolver(ctx context.Context) (Resolver, CollectionStatus, error) {
	if err := ctx.Err(); err != nil {
		return Resolver{}, CollectionStatus{}, err
	}
	raw := C.copyDNSState()
	if raw == nil {
		return Resolver{}, status("unavailable", "SystemConfiguration DNS state unavailable"), nil
	}
	defer C.free(unsafe.Pointer(raw))
	result := parseNativeDNS(C.GoString(raw))
	if err := ctx.Err(); err != nil {
		return Resolver{}, CollectionStatus{}, err
	}
	return result, status("partial", "SystemConfiguration DNS state; supplemental resolver policy is not represented"), nil
}

func parseNativeDNS(text string) Resolver {
	result := Resolver{Servers: []string{}, Search: []string{}, Options: []string{}}
	for _, line := range strings.Split(text, "\n") {
		kind, value, ok := strings.Cut(line, "\t")
		if !ok || value == "" || len(value) > 253 {
			continue
		}
		switch kind {
		case "S":
			if ip := net.ParseIP(value); ip != nil {
				result.Servers = append(result.Servers, ip.String())
			}
		case "D":
			result.Search = append(result.Search, value)
		}
	}
	result.Servers = cleanResolver(result.Servers)
	result.Search = cleanResolver(result.Search)
	return result
}
