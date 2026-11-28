.PHONY: install install-native test test-native-integration test-native-installer test-go test-cross build-native release

GO_CACHE ?= /tmp/ctx-go-build-cache
GO_MOD_CACHE ?= /tmp/ctx-go-mod-cache

install:
	./install.sh

install-native:
	./install-native.sh

build-native:
	mkdir -p dist/build
	GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -o dist/build/ext-native ./cmd/ext
	for adapter in firefox zen floorp waterfox librewolf chrome chromium edge brave safari vivaldi opera whale arc comet dia atlas helium; do GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -o dist/build/ctx-$$adapter-share ./adapters/$$adapter/native || exit; done

release:
	sh ./scripts/build-release-ext.sh "$(VERSION)" "$(or $(DIST),dist/release/$(or $(VERSION),dev))"

test: test-native-integration test-native-installer test-go test-cross

test-native-integration:
	./tests/native_test.sh

test-native-installer:
	./tests/native_installer_test.sh

test-go:
	for module in cmd/ext src/ctx src/ctn src/cto src/cto/sandbox pkg/ext-go/engine pkg/ext-go/cgoengine pkg/ext-go/hashicorp pkg/ext-go/wasm src/ctx/system src/ctx/web; do \
		(cd $$module && GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go test ./...) || exit; \
	done

test-cross:
	mkdir -p dist/build
	GOOS=windows GOARCH=amd64 GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -o dist/build/ext-windows-amd64.exe ./cmd/ext
	for product in ctx ctn cto; do GOOS=windows GOARCH=amd64 GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -o dist/build/$$product-package-windows-amd64.exe ./src/$$product/plugin || exit; done
	for adapter in firefox zen floorp waterfox librewolf chrome chromium edge brave safari vivaldi opera whale arc comet dia atlas helium; do GOOS=windows GOARCH=amd64 GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -o dist/build/ctx-$$adapter-share-windows-amd64.exe ./adapters/$$adapter/native || exit; done
	GOOS=windows GOARCH=arm64 GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -o dist/build/ext-windows-arm64.exe ./cmd/ext
	for product in ctx ctn cto; do GOOS=windows GOARCH=arm64 GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -o dist/build/$$product-package-windows-arm64.exe ./src/$$product/plugin || exit; done
	for adapter in firefox zen floorp waterfox librewolf chrome chromium edge brave safari vivaldi opera whale arc comet dia atlas helium; do GOOS=windows GOARCH=arm64 GOCACHE=$(GO_CACHE) GOMODCACHE=$(GO_MOD_CACHE) go build -o dist/build/ctx-$$adapter-share-windows-arm64.exe ./adapters/$$adapter/native || exit; done
