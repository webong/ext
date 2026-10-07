.PHONY: install install-native test test-native-integration test-native-installer test-go test-cross build-native release

GO_CACHE ?= /tmp/ctx-go-build-cache
GO_MOD_CACHE ?= /tmp/ctx-go-mod-cache

install:
	./install.sh

install-native:
	./install-native.sh

build-native:
	mkdir -p dist/build
	GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -o dist/build/ctx-native ./src/ctx/cmd/ctx
	for adapter in firefox zen floorp waterfox librewolf chrome chromium edge brave safari vivaldi opera whale arc comet dia atlas helium; do GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -o dist/build/ctx-$$adapter-share ./adapters/$$adapter/native || exit; done

release:
	sh ./scripts/build-release.sh "$(VERSION)" "$(or $(DIST),dist/release/$(or $(VERSION),dev))"

test: test-native-integration test-native-installer test-go test-cross

test-native-integration:
	./tests/native_test.sh

test-native-installer:
	./tests/native_installer_test.sh

test-go:
	for module in src/ctx src/ctn examples pkg/plugin-go pkg/plugin-cshared pkg/graph pkg/plugin pkg/plugin-hashicorp pkg/plugin-wasm res/browser res/credential; do \
		(cd $$module && GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go test ./...) || exit; \
	done

test-cross:
	mkdir -p dist/build
	GOOS=windows GOARCH=amd64 GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -o dist/build/ctx-windows-amd64.exe ./src/ctx/cmd/ctx
	for adapter in firefox zen floorp waterfox librewolf chrome chromium edge brave safari vivaldi opera whale arc comet dia atlas helium; do GOOS=windows GOARCH=amd64 GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -o dist/build/ctx-$$adapter-share-windows-amd64.exe ./adapters/$$adapter/native || exit; done
	GOOS=windows GOARCH=arm64 GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -o dist/build/ctx-windows-arm64.exe ./src/ctx/cmd/ctx
	for adapter in firefox zen floorp waterfox librewolf chrome chromium edge brave safari vivaldi opera whale arc comet dia atlas helium; do GOOS=windows GOARCH=arm64 GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -o dist/build/ctx-$$adapter-share-windows-arm64.exe ./adapters/$$adapter/native || exit; done
