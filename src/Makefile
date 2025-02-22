include Makefile.include

DEBIAN_ROOT = ${ROOT}/deb
OUTPUT_DIR = ./builds
GIT_COMMIT = $(shell git rev-parse HEAD | cut -c1-7)
VERSION = 1.0.0-beta
TARGET=gotty
BUILD_OPTIONS = -ldflags "-X main.Version=$(VERSION) -X main.CommitID=$(GIT_COMMIT)"
GO111MODULE=off

ifneq ($(UNAME), Linux)
	PRE_REQ = tools
endif

gotty:	asset ${TARGET}/main.go utils/*.go server/*.go webtty/*.go filebrowser/*.go backend/*.go backend/localcommand/*.go containers/*.go cachedb/*.go user/*.go cookie/*.go ${TARGET}/Makefile
	@echo "building gotty: "${TARGET}
	cd ${TARGET} && \
	make
	mv ${TARGET}/${TARGET} ${ROOT}/bin/
	#godep go build ${BUILD_OPTIONS}

.PHONY: asset
asset: bindata/static/js/gotty-bundle.js bindata/static/js/chat-widget.js bindata/static/js/jsconsole.js bindata/static/index.html bindata/static/doc.html bindata/static/editblog.html bindata/static/about.html bindata/static/NewFile.html bindata/static/images bindata/static/css bindata/static/css/xterm.css bindata/static/meta
	$(ROOT)/bin/go-bindata -prefix bindata -pkg server -ignore=\\.gitkeep -o server/asset.go bindata/...
	GO111MODULE=off $(GOROOT)/bin/gofmt -w server/asset.go

.PHONY: all
all: $(PRE_REQ) clean asset gotty

bindata:
	mkdir bindata

bindata/static: bindata
	mkdir bindata/static

bindata/static/index.html: bindata/static resources/index.html resources/profile.html resources/practice.html resources/robots.txt jsconsole/build/static/jsconsole.html
	cp resources/index.html bindata/static/index.html
	cp resources/profile.html bindata/static/profile.html
	cp resources/practice.html bindata/static/practice.html
	cp resources/robots.txt bindata/static/robots.txt
	cp jsconsole/build/static/jsconsole.html bindata/static/jsconsole.html

bindata/static/docs: bindata/static resources/docs
	mkdir -p bindata/static/docs
	cp -Rv resources/docs/* bindata/static/docs/

bindata/static/meta: bindata/static resources/meta/*
	mkdir -p bindata/static/meta
	cp -Rv resources/meta/* bindata/static/meta/

bindata/static/doc.html: bindata/static resources/doc.html bindata/static/docs
	cp resources/doc.html bindata/static/doc.html

bindata/static/editblog.html: bindata/static resources/editblog.html
	cp resources/editblog.html bindata/static/editblog.html

bindata/static/about.html: bindata/static resources/about.html resources/references.html
	cp resources/about.html bindata/static/about.html
	cp resources/references.html bindata/static/references.html

bindata/static/NewFile.html: bindata/static resources/NewFile.html
	cp resources/NewFile.html bindata/static/NewFile.html

bindata/static/images: bindata/static resources/images jsconsole/build/static/media
	mkdir -p bindata/static/images
	mkdir -p bindata/static/media
	cp -Rv resources/images/* bindata/static/images/
	cp -Rv jsconsole/build/static/media/* bindata/static/media/

bindata/static/js: bindata/static resources/js
	mkdir -p bindata/static/js
	cp -Rv resources/js/* bindata/static/js/

bindata/static/js/gotty-bundle.js: bindata/static/js js/dist/gotty-bundle.js js/dist/preprocessing.js
	cp js/dist/gotty-bundle.js bindata/static/js/gotty-bundle.js
	cp js/dist/preprocessing.js bindata/static/js/preprocessing.js

bindata/static/css: bindata/static resources/css/* jsconsole/build/static/css/jsconsole.css
	mkdir -p bindata/static/css
	cp -Rv resources/css/* bindata/static/css/
	cp jsconsole/build/static/css/jsconsole.css bindata/static/css/jsconsole.css

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

jsconsole/node_modules/webpack:
	@echo "building jsconsole: "
	cd jsconsole && \
	npm install

JS_FILES := $(wildcard jsconsole/*) $(wildcard jsconsole/*/*) $(wildcard jsconsole/*/*/*) 

jsconsole/build/static/js/jsconsole.js: jsconsole/node_modules/webpack $(JS_FILES)
	cd jsconsole && \
	NODE_ENV=production REACT_APP_VERSION=${VERSION} `npm bin`/webpack

bindata/static/js/jsconsole.js: jsconsole/build/static/js/jsconsole.js
	cp jsconsole/build/static/js/jsconsole.js bindata/static/js/jsconsole.js

CHAT_JS_FILES := $(wildcard resources/chat-widget/src/widget.*) $(wildcard resources/chat-widget/*.json) $(wildcard resources/chat-widget/*.js) resources/chat-widget/src/index.ts resources/chat-widget/src/openreplkeywords.ts

resources/chat-widget/dist/index.umd.js: $(CHAT_JS_FILES)
	@echo "building chat-widget: "
	cd resources/chat-widget && \
	npm install

bindata/static/js/chat-widget.js: resources/chat-widget/dist/index.umd.js
	cp resources/chat-widget/dist/index.umd.js bindata/static/js/chat-widget.js

deb: all
	mkdir -p ${DEBIAN_ROOT}/gotty/usr/lib/systemd/system
	mkdir -p ${DEBIAN_ROOT}/gotty/usr/local/bin
	install -m 644 ${ROOT}/src/services/gotty.service ${DEBIAN_ROOT}/gotty/usr/lib/systemd/system/gotty.service
	install -m 755 ${ROOT}/bin/gotty ${DEBIAN_ROOT}/gotty/usr/local/bin/gotty
	cd ${DEBIAN_ROOT} && dpkg-deb --build gotty/

tools:
	GO111MODULE=off $(GOROOT)/bin/go get github.com/tools/godep
	GO111MODULE=off $(GOROOT)/bin/go get github.com/mitchellh/gox
	GO111MODULE=off $(GOROOT)/bin/go get github.com/tcnksm/ghr
	GO111MODULE=off $(GOROOT)/bin/go get github.com/jteeuwen/go-bindata/...

gobuild:
	@echo "building " ${GOROOT}
	@echo "using bootstrap " ${GOROOT_BOOTSTRAP}
	cd ${GOROOT}/src && \
	./all.bash || true

buildgo: gobuild tools

clean:
	@echo ""
	@echo ".... Cleaning " ${TARGET}
	cd ${TARGET} && \
	make clean
	rm -rf bindata
	rm -rf js/dist/*
	@echo ".... Clean Done"

cleanjs:
	rm -rf js/node_modules
	rm -rf jsconsole/node_modules
	rm -rf resources/chat-widget/node_modules
	rm -rf js/dist/*
	rm -rf resources/chat-widget/dist/*
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
