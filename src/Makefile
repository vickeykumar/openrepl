include Makefile.include

DEBIAN_ROOT = ${ROOT}/deb
OUTPUT_DIR = ./builds
GIT_COMMIT = $(shell git rev-parse HEAD | cut -c1-7)
VERSION = 2.0.0-alpha.3
TARGET=gotty
BUILD_OPTIONS = -ldflags "-X main.Version=$(VERSION) -X main.CommitID=$(GIT_COMMIT)"

ifneq ($(UNAME), Linux)
	PRE_REQ = tools
endif

gotty:	${TARGET}/main.go server/*.go webtty/*.go backend/*.go ${TARGET}/Makefile
	@echo "building gotty: "${TARGET}
	cd ${TARGET} && \
	make
	mv ${TARGET}/${TARGET} ${ROOT}/bin/
	#godep go build ${BUILD_OPTIONS}

.PHONY: asset
asset: bindata/static/js/gotty-bundle.js bindata/static/index.html bindata/static/favicon.png bindata/static/css/index.css bindata/static/css/xterm.css bindata/static/css/xterm_customize.css
	$(ROOT)/bin/go-bindata -prefix bindata -pkg server -ignore=\\.gitkeep -o server/asset.go bindata/...
	$(GOROOT)/bin/gofmt -w server/asset.go

.PHONY: all
all: $(PRE_REQ) asset gotty

bindata:
	mkdir bindata

bindata/static: bindata
	mkdir bindata/static

bindata/static/index.html: bindata/static resources/index.html
	cp resources/index.html bindata/static/index.html

bindata/static/favicon.png: bindata/static resources/favicon.png
	cp resources/favicon.png bindata/static/favicon.png

bindata/static/js: bindata/static
	mkdir -p bindata/static/js


bindata/static/js/gotty-bundle.js: bindata/static/js js/dist/gotty-bundle.js
	cp js/dist/gotty-bundle.js bindata/static/js/gotty-bundle.js

bindata/static/css: bindata/static
	mkdir -p bindata/static/css

bindata/static/css/index.css: bindata/static/css resources/index.css
	cp resources/index.css bindata/static/css/index.css

bindata/static/css/xterm_customize.css: bindata/static/css resources/xterm_customize.css
	cp resources/xterm_customize.css bindata/static/css/xterm_customize.css

bindata/static/css/xterm.css: bindata/static/css js/node_modules/xterm/dist/xterm.css
	cp js/node_modules/xterm/dist/xterm.css bindata/static/css/xterm.css

js/node_modules/xterm/dist/xterm.css:
	cd js && \
	npm install

js/dist/gotty-bundle.js: js/src/* js/node_modules/webpack
	cd js && \
	`npm bin`/webpack

js/node_modules/webpack:
	cd js && \
	npm install

deb: all
	mkdir -p ${DEBIAN_ROOT}/gotty/usr/lib/systemd/system
	mkdir -p ${DEBIAN_ROOT}/gotty/usr/local/bin
	install -m 644 ${ROOT}/src/services/gotty.service ${DEBIAN_ROOT}/gotty/usr/lib/systemd/system/gotty.service
	install -m 755 ${ROOT}/bin/gotty ${DEBIAN_ROOT}/gotty/usr/local/bin/gotty
	cd ${DEBIAN_ROOT} && dpkg-deb --build gotty/

tools:
	$(GOROOT)/bin/go get github.com/tools/godep
	$(GOROOT)/bin/go get github.com/mitchellh/gox
	$(GOROOT)/bin/go get github.com/tcnksm/ghr
	$(GOROOT)/bin/go get github.com/jteeuwen/go-bindata/...

clean:
	@echo ""
	@echo ".... Cleaning " ${TARGET}
	cd ${TARGET} && \
	make clean
	@echo ".... Clean Done"

test:
	if [ `go fmt $(go list ./... | grep -v /vendor/) | wc -l` -gt 0 ]; then echo "go fmt error"; exit 1; fi

cross_compile:
	GOARM=5 gox -os="darwin linux freebsd netbsd openbsd" -arch="386 amd64 arm" -osarch="!darwin/arm" -output "${OUTPUT_DIR}/pkg/{{.OS}}_{{.Arch}}/{{.Dir}}"

targz:
	mkdir -p ${OUTPUT_DIR}/dist
	cd ${OUTPUT_DIR}/pkg/; for osarch in *; do (cd $$osarch; tar zcvf ../../dist/gotty_${VERSION}_$$osarch.tar.gz ./*); done;

shasums:
	cd ${OUTPUT_DIR}/dist; sha256sum * > ./SHA256SUMS

release:
	ghr -c ${GIT_COMMIT} --delete --prerelease -u yudai -r gotty pre-release ${OUTPUT_DIR}/dist
