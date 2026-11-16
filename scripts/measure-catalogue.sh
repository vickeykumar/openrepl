#!/bin/bash
# Measures every entry of the catalogue inside the image, for the CPU type of this machine, and writes the options and sizes.
#   scripts/measure-catalogue.sh [image] [out-dir]     (defaults: openrepl:arm64 on arm, openrepl:amd64 on amd64; ./measure)
#   OPENREPL_MEASURE_ONLY=vim,tmux   OPENREPL_MEASURE_KINDS=tool   to measure a part
# Then, with the files of both CPU types:  go run appstore/tools/merge -catalogue appstore/catalogue_default.json measure/measure-*.json
# The job "App Store installs" of the CI runs the same program, so the numbers are the ones of the image users get.
set -e
cd "$(dirname "$0")/.."
case "$(uname -m)" in arm64|aarch64) A=arm64; ARCH=linux-aarch64;; *) A=amd64; ARCH=linux-64;; esac
IMAGE=${1:-openrepl:$A}
OUT=${2:-$PWD/measure}
mkdir -p "$OUT"
# the program is built with the Go of the repository (go_1.23), inside the image, so that it is the one CI builds: static, no cgo
docker run --rm --entrypoint bash -v "$PWD:/opt/openrepl" -v "$OUT:/out" -w /opt/openrepl/src "$IMAGE" -lc \
  "export PATH=/opt/openrepl/go_1.23/$(uname -m | sed s/arm64/aarch64/)/go/bin:\$PATH GO111MODULE=off GOPATH=/opt/openrepl CGO_ENABLED=0; go test -tags realinstall -c -o /out/appstore.test appstore"
docker run --rm --entrypoint /appstore.test -v "$OUT/appstore.test:/appstore.test:ro" -v "$OUT:/out" \
  -e OPENREPL_MEASURE_OUT=/out/measure-$ARCH.json -e OPENREPL_REAL_STORE=/store \
  -e OPENREPL_MEASURE_ONLY="$OPENREPL_MEASURE_ONLY" -e OPENREPL_MEASURE_KINDS="$OPENREPL_MEASURE_KINDS" \
  "$IMAGE" -test.v -test.timeout=600m -test.run=TestMeasureCatalogue
