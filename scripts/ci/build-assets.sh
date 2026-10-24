#!/usr/bin/env bash
# Makes src/server/asset.go for the Go tests, the way `make asset` does (src/Makefile),
# without building the JS console (jsconsole, an old React app that only the Docker
# build needs): its three files are stand-ins. Run it from anywhere after the page
# bundle (src/js: npx webpack) and the Genie widget (src/resources/chat-widget: npm ci)
# have been built.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT/src"

need() { [ -e "$1" ] || { echo "missing $1 (build the page bundle and the widget first)"; exit 1; }; }
need js/dist/gotty-bundle.js
need js/dist/hterm.js
need js/dist/scribbler.js
need js/dist/preprocessing.js
need resources/chat-widget/dist/index.umd.js
need js/node_modules/@xterm/xterm/css/xterm.css

rm -rf bindata
mkdir -p bindata/static/js bindata/static/css bindata/static/images bindata/static/media bindata/static/meta bindata/static/docs bindata/knowledge

# pages
for f in index profile practice doc editblog admin about references privacy terms NewFile; do
  cp "resources/$f.html" "bindata/static/$f.html"
done
cp resources/robots.txt bindata/static/robots.txt
echo '<!doctype html><title>jsconsole (stand-in for the tests)</title>' > bindata/static/jsconsole.html

# docs, notes, meta, images, scripts, styles
[ -d resources/docs ] && cp -R resources/docs/. bindata/static/docs/
cp -R resources/knowledge/. bindata/knowledge/
cp -R resources/meta/. bindata/static/meta/
cp -R resources/images/. bindata/static/images/
cp -R resources/js/. bindata/static/js/
cp -R resources/css/. bindata/static/css/
cp js/node_modules/@xterm/xterm/css/xterm.css bindata/static/css/xterm.css

# bundles
cp js/dist/gotty-bundle.js js/dist/hterm.js js/dist/scribbler.js js/dist/preprocessing.js bindata/static/js/
cp resources/chat-widget/dist/index.umd.js bindata/static/js/chat-widget.js

# the jsconsole files: long enough for the "the binary carries it" test (100 bytes)
STAND_IN='/* jsconsole stand-in for the Go tests: the real one is built by the Docker image (make asset). ................ */'
echo "$STAND_IN" > bindata/static/js/jsconsole.js
echo "$STAND_IN" > bindata/static/css/jsconsole.css
touch bindata/static/media/.gitkeep

"$ROOT/bin/go-bindata" -prefix bindata -pkg server -ignore='\.gitkeep' -o server/asset.go bindata/...
gofmt -w server/asset.go
echo "server/asset.go written: $(grep -c 'bindataFileInfo{' server/asset.go) files"
