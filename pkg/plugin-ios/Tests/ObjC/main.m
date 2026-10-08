// An Objective-C program using the guest SDK and the web-view host through their
// EXT* facades, and the ext_plugin_* C ABI an Objective-C plugin would export.
// scripts/plugin-ios-objc.sh builds and runs it.
#import <Foundation/Foundation.h>
#import "ExtPluginExports-Swift.h"
#import "ExtPluginGuest-Swift.h"
#import "ExtPluginWebView-Swift.h"

uint32_t ext_plugin_abi_version(void);
uint64_t ext_plugin_open(void);
uint32_t ext_plugin_call(uint64_t handle, uint32_t operation, const uint8_t *request, uint32_t request_len,
                         uint8_t *response, uint32_t capacity, uint32_t *written);
void ext_plugin_close(uint64_t handle);

static int failures = 0;
#define CHECK(condition, ...) do { if (!(condition)) { failures++; NSLog(@"FAIL: " __VA_ARGS__); } } while (0)

static EXTGuest *MakeGuest(void) {
    NSError *error = nil;
    NSData *(^handler)(EXTCall *, NSError **) = ^NSData *(EXTCall *call, NSError **errorOut) {
        if ([call.operation isEqualToString:@"busy"]) {
            *errorOut = [EXTGuest remoteErrorWithCode:@"busy" message:@"try later" retryAfterMilliseconds:10];
            return nil;
        }
        if ([call.operation isEqualToString:@"boom"]) {
            *errorOut = [NSError errorWithDomain:@"secret" code:1 userInfo:nil];
            return nil;
        }
        return call.payload;
    };
    EXTGuest *guest = [[EXTGuest alloc]
        initWithIdentifier:@"objc/test" revision:@"r1" contractName:@"test" contractVersion:@"v1"
        operations:@[@"echo", @"busy", @"boom"] error:&error handler:handler];
    CHECK(guest != nil, @"guest: %@", error);
    return guest;
}

// The one function an Objective-C plugin defines to be loadable through the C ABI.
void *ext_plugin_guest_factory(void) {
    return [EXTPluginExport retainedPointerFor:MakeGuest()];
}

static NSData *Request(NSString *operation, NSString *payload) {
    NSString *body = [NSString stringWithFormat:
        @"{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\",\"plugin\":{\"id\":\"objc/test\",\"revision\":\"r1\"},"
        @"\"contract\":{\"name\":\"test\",\"version\":\"v1\"},\"operation\":\"%@\",\"deadline\":\"2099-01-01T00:00:00Z\"%@}",
        operation, payload ? [NSString stringWithFormat:@",\"payload\":%@", payload] : @""];
    return [body dataUsingEncoding:NSUTF8StringEncoding];
}

static NSString *Text(NSData *data) { return [[NSString alloc] initWithData:data encoding:NSUTF8StringEncoding]; }

static void TestGuest(void) {
    EXTGuest *guest = MakeGuest();
    NSError *error = nil;
    NSString *descriptor = Text([guest descriptorJSONAndReturnError:&error]);
    CHECK([descriptor containsString:@"\"id\":\"objc/test\""], @"descriptor %@ %@", descriptor, error);
    NSString *echo = Text([guest invoke:Request(@"echo", @"{\"n\":12345678901234567890,\"s\":\"héllo\"}") timeoutMilliseconds:5000 error:&error]);
    CHECK([echo containsString:@"12345678901234567890"] && [echo containsString:@"héllo"], @"echo %@ %@", echo, error);
    NSString *busy = Text([guest invoke:Request(@"busy", nil) timeoutMilliseconds:5000 error:&error]);
    CHECK([busy containsString:@"\"code\":\"busy\""] && [busy containsString:@"\"retryAfterMilliseconds\":10"], @"busy %@", busy);
    NSString *boom = Text([guest invoke:Request(@"boom", nil) timeoutMilliseconds:5000 error:&error]);
    CHECK([boom containsString:@"operation_failed"] && ![boom containsString:@"secret"], @"boom %@", boom);
    NSData *bad = [guest invoke:[@"{" dataUsingEncoding:NSUTF8StringEncoding] timeoutMilliseconds:5000 error:&error];
    CHECK(bad == nil && error != nil, @"malformed request must fail");
}

static void TestCABI(void) {
    CHECK(ext_plugin_abi_version() == 1, @"abi version");
    uint64_t handle = ext_plugin_open();
    CHECK(handle != 0, @"open");
    uint32_t capacity = 24u * 1024u * 1024u, written = 0;
    uint8_t *response = malloc(capacity);
    const char *hello = "{\"deadline\":\"2099-01-01T00:00:00Z\"}";
    CHECK(ext_plugin_call(handle, 1, (const uint8_t *)hello, (uint32_t)strlen(hello), response, capacity, &written) == 0, @"handshake");
    NSString *descriptor = Text([NSData dataWithBytes:response length:written]);
    CHECK([descriptor containsString:@"objc/test"], @"descriptor over the C ABI: %@", descriptor);
    NSData *request = Request(@"echo", @"{\"v\":7}");
    CHECK(ext_plugin_call(handle, 2, request.bytes, (uint32_t)request.length, response, capacity, &written) == 0, @"invoke");
    NSString *reply = Text([NSData dataWithBytes:response length:written]);
    CHECK([reply containsString:@"\"payload\":{\"v\":7}"], @"invoke over the C ABI: %@", reply);
    ext_plugin_close(handle);
    free(response);
}

static void Spin(BOOL *done, NSTimeInterval seconds) {
    NSDate *limit = [NSDate dateWithTimeIntervalSinceNow:seconds];
    while (!*done && [limit timeIntervalSinceNow] > 0) {
        [[NSRunLoop currentRunLoop] runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.05]];
    }
}

static void TestWebView(NSData *module) {
    EXTWebViewPluginHost *host = [[EXTWebViewPluginHost alloc] initWithModule:module bridge:nil log:nil];
    __block BOOL done = NO;
    __block NSError *startError = nil;
    [host startWithTimeout:120 completion:^(NSError *error) { startError = error; done = YES; }];
    Spin(&done, 130);
    CHECK(done && startError == nil, @"start: %@", startError);
    if (startError) return;
    done = NO;
    __block NSData *descriptor = nil;
    [host handshakeWithTimeout:10 completion:^(NSData *data, NSError *error) { descriptor = data; done = YES; }];
    Spin(&done, 20);
    CHECK([Text(descriptor) containsString:@"ctx/conformance"], @"web view descriptor %@", Text(descriptor));
    done = NO;
    NSString *body = @"{\"apiVersion\":\"ext.plugin/v1\",\"id\":\"1\",\"plugin\":{\"id\":\"ctx/conformance\",\"revision\":\"fixture-1\"},"
                     @"\"contract\":{\"name\":\"ext.conformance\",\"version\":\"v1\"},\"operation\":\"echo\",\"deadline\":\"2099-01-01T00:00:00Z\",\"payload\":{\"v\":7}}";
    __block NSData *reply = nil;
    [host invoke:[body dataUsingEncoding:NSUTF8StringEncoding] timeout:10 completion:^(NSData *data, NSError *error) { reply = data; done = YES; }];
    Spin(&done, 20);
    CHECK([Text(reply) containsString:@"\"payload\""] && [Text(reply) containsString:@"7"], @"web view echo %@", Text(reply));
    [host close];
    done = NO;
    __block NSError *closedError = nil;
    [host handshakeWithTimeout:5 completion:^(NSData *data, NSError *error) { closedError = error; done = YES; }];
    Spin(&done, 10);
    CHECK(closedError.code == EXTWebViewPluginHostErrorClosed, @"closed host error %@", closedError);
}

int main(void) {
    @autoreleasepool {
        TestGuest();
        TestCABI();
        const char *path = getenv("EXT_RUST_REACTOR");
        if (path && *path) {
            TestWebView([NSData dataWithContentsOfFile:[NSString stringWithUTF8String:path]]);
        } else {
            NSLog(@"web view check skipped: set EXT_RUST_REACTOR");
        }
        NSLog(failures == 0 ? @"all Objective-C checks passed" : @"%d Objective-C checks FAILED", failures);
    }
    return failures == 0 ? 0 : 1;
}
