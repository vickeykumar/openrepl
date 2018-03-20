include ../Makefile.include

GIT_COMMIT = $(shell git rev-parse HEAD | cut -c1-7)
VERSION = 2.0.0-alpha.3
TARGET=gotty
BUILD_OPTIONS = -ldflags "-X main.Version=$(VERSION) -X main.CommitID=${GIT_COMMIT}"

gotty: main.go ../server/*.go ../webtty/*.go ../backend/*.go Makefile
	@echo "compiling: "
	$(GOROOT)/bin/go build ${BUILD_OPTIONS} -x -v ${TARGET}
	@echo "compilation done."
	#godep go build ${BUILD_OPTIONS}


clean:
	@echo ""
	@echo ".... Cleaning " ${TARGET}
	${GOROOT}/bin/go clean -x ${TARGET}
	@echo ".... Clean Done"

