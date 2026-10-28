#!/usr/bin/env bash
# please run as sudo

GOTTY_DIR=/opt/gotty 
mkdir -p $GOTTY_DIR || true
chmod 644 $GOTTY_DIR
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Use the Debian package architecture, which describes the image being built and
# is more reliable than uname -m when the build runs under emulation.
DEB_ARCH=$(dpkg --print-architecture)
case "$DEB_ARCH" in
    amd64) GO_ARCH=amd64; EVCXR_TARGET=x86_64-unknown-linux-gnu ;;
    arm64) GO_ARCH=arm64; EVCXR_TARGET=aarch64-unknown-linux-gnu ;;
    *) echo "Unsupported Debian architecture: $DEB_ARCH (supported: amd64, arm64)" >&2; exit 1 ;;
esac

retVal=0

display_usage() {
  echo "Usage: $0 [--cleanup-tools] [--run-tests] [--help]"
  echo "Options:"
  echo "  --cleanup-tools        Cleanup tools after REPLs installation"
  echo "  --run-tests            Run tests"
  echo "  --help                 Display this help message"
  echo "  --username USERNAME    Specify the username for installation"
}

cleanup_tools=0
run_tests=0
username=$(whoami)

# Parse command-line arguments
while [[ "$#" -gt 0 ]]; do
  case $1 in
    --cleanup-tools) cleanup_tools=1; shift ;;
    --run-tests) run_tests=1; shift ;;
    --help) display_usage; exit 0 ;;
    --username)
      if [[ -n "$2" && "$2" != --* ]]; then
        username="$2"; shift 2
      else
        echo "Error: --username requires a value"
        display_usage
        exit 1
      fi
      ;;
    *)
      echo "Invalid argument: $1"
      display_usage
      exit 1
      ;;
  esac
done

echo "running script from : " $SCRIPT_DIR
echo "username: " $username

cd ~

#export DEBIAN_FRONTEND=noninteractive
export TZ=Etc/UTC

apt-get update -y
apt-get install -y --no-install-recommends make gcc g++ default-jdk git
# the Java REPL is jshell (utils/interactive.go): part of the JDK since 9, but fail early if it is missing
command -v jshell >/dev/null || { echo "jshell was not installed with default-jdk"; exit 1; }

#install ipython2.7
apt-get install -y --no-install-recommends python2.7
ln -s /usr/bin/python2.7 /usr/bin/python
apt-get install -y --no-install-recommends ipython
apt-get install -y --no-install-recommends python-is-python3

#install ipython3
apt-get install -y --no-install-recommends ipython3


# set cap to nsenter,gcc,g++
apt-get install -y --no-install-recommends libcap2-bin
setcap "cap_sys_admin,cap_sys_ptrace+ep" /usr/bin/nsenter 
chown root.root /usr/bin/nsenter
chmod 4755 /usr/bin/nsenter
# && ./containers-from-scratch/main run 2 nsenter -n -t$$ /bin/bash
# sudo setcap "cap_sys_admin,cap_sys_ptrace+ep" /usr/bin/arm-linux-gnueabihf-gcc-8
# add export NODE_OPTIONS=--max_old_space_size=2048 to .bashrc
apt-get install -y --no-install-recommends net-tools

#install golang
apt-get install -y --no-install-recommends golang

#install yaegi go repl, >= go1.18
#go install github.com/traefik/yaegi/cmd/yaegi@latest
apt-get install -y --no-install-recommends yaegi

#install npm
apt-get install -y --no-install-recommends npm
#install the last stable release of npm and node for this project using nvm
# node v12.22.9
cd ~
apt-get -y --no-install-recommends install curl wget bzip2
echo "home: " $HOME
curl -sL https://raw.githubusercontent.com/nvm-sh/nvm/v0.35.0/install.sh -o $HOME/install_nvm.sh 
chmod 755 $HOME/install_nvm.sh
source $HOME/install_nvm.sh
source $HOME/.bashrc

# ENV for nvm
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"  # This loads nvm
# nodejs.org is sometimes unreachable from a CI runner for a few minutes: try again before giving up
for attempt in 1 2 3; do
    nvm install v12.22.9
    [ -x "$NVM_DIR/versions/node/v12.22.9/bin/node" ] && break
    echo "nvm install v12.22.9 failed (attempt $attempt of 3)" >&2
    sleep 15
done
npm install npm@8.5.1 -g

#install C++ REPL
#use following to compile minimal cling
#/usr/bin/ld.gold --strip-all --no-map-whole-files --no-keep-memory --no-keep-files-mapped $@ 
# cling takes some time to init first instance, add below lines to rc.local(startup)
#/usr/bin/cling 21321 .q > /dev/null 2>&1 &
cd $GOTTY_DIR

if [ -e "/usr/local/bin/cling" ]; then
    echo "File /usr/local/bin/cling exists."
else
    echo "File /usr/local/bin/cling does not exist. installing..."
    if [ "$DEB_ARCH" = amd64 ]; then
        CLING_ARCHIVE=cling-Ubuntu-22.04-x86_64-1.0~dev-d47b49c.tar.bz2
        wget "https://raw.githubusercontent.com/vickeykumar/openrepl/e596c6f0918e48eeba7a0bf7b7d2632f6b155ffb/repls/$CLING_ARCHIVE"
        tar -xvf "$CLING_ARCHIVE"
        chmod 755 "$GOTTY_DIR/cling-Ubuntu-22.04-x86_64-1.0~dev-d47b49c/bin/cling"
        ln -s "$GOTTY_DIR/cling-Ubuntu-22.04-x86_64-1.0~dev-d47b49c/bin/cling" /usr/local/bin/cling
        chmod 755 /usr/local/bin/cling
        rm "$CLING_ARCHIVE"
    else
        # No arm64 build of the bundled Cling archive exists, but conda-forge
        # publishes Cling for linux-aarch64 (Ubuntu's clang-repl takes declarations
        # only and has no meta-commands, so it cannot stand in for it). micromamba
        # installs it, then the build-only parts (static LLVM libraries, headers,
        # cmake files, the package cache) are deleted: 2.9 GB becomes about 1.6 GB.
        CLING_PREFIX=/opt/cling
        MAMBA_DIR=$(mktemp -d)
        # keep micromamba's package cache (about as large as the install) in MAMBA_DIR, so removing it
        # below removes the cache too; by default it goes to ~/.local/share/mamba and stays in the image
        export MAMBA_ROOT_PREFIX="$MAMBA_DIR/root"
        curl -fsSL https://micro.mamba.pm/api/micromamba/linux-aarch64/latest | tar -C "$MAMBA_DIR" -xj bin/micromamba
        "$MAMBA_DIR/bin/micromamba" create -y -q -p "$CLING_PREFIX" -c conda-forge --override-channels cling=1.2
        find "$CLING_PREFIX" -name '*.a' -delete
        rm -rf "$CLING_PREFIX/conda-meta" "$CLING_PREFIX/pkgs" "$CLING_PREFIX/lib/cmake" \
            "$CLING_PREFIX"/include/llvm* "$CLING_PREFIX"/include/mlir* "$MAMBA_DIR"
        unset MAMBA_ROOT_PREFIX
        ln -s "$CLING_PREFIX/bin/cling" /usr/local/bin/cling
        chmod 755 /usr/local/bin/cling
    fi
fi

#install evcxr and set all the required dependencies
if [ -e "/usr/local/bin/evcxr" ]; then
    echo "File /usr/local/bin/evcxr exists."
else
    echo "File /usr/local/bin/evcxr does not exist. installing..."
    apt-get install -y --no-install-recommends rustc
    apt-get install -y --no-install-recommends rust-gdb
    apt-get install -y --no-install-recommends cargo
    if [ "$DEB_ARCH" = amd64 ]; then
        EVCXR_ARCHIVE="evcxr-v0.17.0-$EVCXR_TARGET.tar.gz"
        wget "https://github.com/evcxr/evcxr/releases/download/v0.17.0/$EVCXR_ARCHIVE"
        tar -xvf "$EVCXR_ARCHIVE"
        cp "evcxr-v0.17.0-$EVCXR_TARGET/evcxr" /usr/local/bin/evcxr
        chmod 755 /usr/local/bin/evcxr
        rm "$EVCXR_ARCHIVE"
        rm -rf "evcxr-v0.17.0-$EVCXR_TARGET"
    else
        # Build natively because the upstream prebuilt asset is x86_64 only.
        curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --profile minimal --default-toolchain 1.74.1
        . "$HOME/.cargo/env"
        cargo install --locked --version 0.17.0 evcxr_repl
        ln -sf "$HOME/.cargo/bin/evcxr" /usr/local/bin/evcxr
    fi
fi

#install rappel, nasm and set all the required dependencies
if [ -e "/usr/local/bin/rappel" ]; then
    echo "File /usr/local/bin/rappel exists."
else
    echo "File /usr/local/bin/rappel does not exist. installing..."
    apt-get install -y --no-install-recommends libedit-dev
    apt-get install -y --no-install-recommends nasm
    git clone https://github.com/yrp604/rappel.git
    cd rappel
    make
    ln -s $GOTTY_DIR/rappel/bin/rappel /usr/local/bin/rappel
    chmod 755 /usr/local/bin/rappel
    cd ..
fi
# rappel traces its child with ptrace: on a host without it the wrapper says so, and
# starts the real rappel (OPENREPL_RAPPEL_BIN) everywhere else
if [ -x "$GOTTY_DIR/rappel/bin/rappel" ]; then
    if [ -f "$SCRIPT_DIR/scripts/openrepl-rappel" ]; then
        rm -f /usr/local/bin/rappel
        install -m 755 "$SCRIPT_DIR/scripts/openrepl-rappel" /usr/local/bin/rappel
    else
        echo "ERROR: $SCRIPT_DIR/scripts/openrepl-rappel is missing"
        retVal=1
    fi
fi

#install gomacro, the Go REPL. It is pure Go and needs no Go toolchain at run time, but its go.mod
#asks for Go 1.23, newer than the apt golang: build it with a toolchain downloaded for this step
#only (the commit is pinned: its tags stop at 2018), then delete that toolchain.
GOMACRO_REV=0df6c0cd20af13f83b0203c64a16ba08dda38c02
GOMACRO_GO=go1.23.12
GOMACRO_ARCH=$GO_ARCH
GOMACRO_DIR=$(mktemp -d)
if curl -fsSL "https://go.dev/dl/$GOMACRO_GO.linux-$GOMACRO_ARCH.tar.gz" | tar -C "$GOMACRO_DIR" -xz \
    && env -u GOROOT GO111MODULE=on GOFLAGS= GOTOOLCHAIN=local GOPATH="$GOMACRO_DIR/gopath" GOBIN=/usr/local/bin \
        "$GOMACRO_DIR/go/bin/go" install "github.com/cosmos72/gomacro@$GOMACRO_REV"; then
    echo "gomacro installed: $(ls -la /usr/local/bin/gomacro)"
else
    echo "ERROR: gomacro could not be built"
    retVal=1
fi
chmod -R u+w "$GOMACRO_DIR" && rm -rf "$GOMACRO_DIR"

#install jq-repl
apt-get install -y --no-install-recommends jq
git clone https://github.com/vickeykumar/jq-repl.git
cd jq-repl
make install
cd ..

#install Ruby(irb)
apt-get install -y --no-install-recommends ruby

#install perli
apt-get install -y --no-install-recommends rlwrap
# append alias to the /etc/profile
# alias yaegi="rlwrap yaegi"

apt-get install -y --no-install-recommends perl
apt-get install -y --no-install-recommends perl-doc
git clone https://github.com/vickeykumar/perli.git
cd perli && make install
cd ~

#install tcl
apt-get install -y --no-install-recommends tcl

#install sqlite3
apt-get install -y --no-install-recommends sqlite3

#install typescript and ts-node
npm install -g typescript@4.9.5
npm install -g ts-node@10.9.2
npm link typescript
# ts-node brings its own copy of the newest typescript (7.x, the native compiler, which has no
# JavaScript API: no `ts.sys`) and loads that one before the global typescript 4.9.5, so the
# TypeScript REPL stopped with "Cannot read property 'fileExists' of undefined". Remove the copy:
# it then finds the global one.
rm -rf "$(npm root -g)/ts-node/node_modules/typescript"

# node, npm, npx, tsc and ts-node by name, for the server and the REPLs it starts. nvm puts its
# folder on the PATH of an interactive shell only; the server has the plain system PATH. The apt
# nodejs (the same 12.22.9) is removed by the cleanup below, so without these links the image has
# no `node` at all: the Node.js and TypeScript REPLs answered "executable file not found in $PATH".
NODE_BIN_DIR="$HOME/.nvm/versions/node/v12.22.9/bin"
if [ -x "$NODE_BIN_DIR/node" ]; then
    for f in "$NODE_BIN_DIR"/*; do
        ln -sf "$f" "/usr/local/bin/$(basename "$f")"
    done
else
    echo "ERROR: $NODE_BIN_DIR/node is missing: nvm did not install node"
    retVal=1
fi

# currently stable gdb version is 8.1.1, can be copied from bin/ build from src
#apt-get install -y --no-install-recommends gdb
if [ "$DEB_ARCH" = amd64 ]; then
    cp "$SCRIPT_DIR/bin/gdb" /usr/local/bin/ || true
    chmod 755 /usr/local/bin/gdb || true
fi

# Debug (gdb) needs ptrace, and some hosts do not have it: the Raspberry Pi workers
# (an amd64 image under qemu-user), x86 code under Rosetta, sandboxes. There gdb
# talks to qemu instead, which runs the program and serves gdb on a port:
#   openrepl-ptrace-probe   asks the host whether ptrace works (built once, here)
#   openrepl-gdb            what the Debug button starts: gdb, or gdb + qemu
apt-get install -y --no-install-recommends qemu-user
# qemu describes its registers to gdb in XML, so that route needs a gdb built with XML
# support; the bundled gdb 8.1.1 (above, first on the PATH) is not. Ubuntu's is.
apt-get install -y --no-install-recommends gdb
if [ -f "$SCRIPT_DIR/scripts/ptrace-probe.c" ] && [ -f "$SCRIPT_DIR/scripts/openrepl-gdb" ]; then
    gcc -O1 -o /usr/local/bin/openrepl-ptrace-probe "$SCRIPT_DIR/scripts/ptrace-probe.c" || retVal=1
    install -m 755 "$SCRIPT_DIR/scripts/openrepl-gdb" /usr/local/bin/openrepl-gdb || retVal=1
else
    echo "ERROR: $SCRIPT_DIR/scripts/ptrace-probe.c or openrepl-gdb is missing"
    retVal=1
fi
chmod -R 777 /tmp/home || true

#cleanup
# remove tools used in between to optimize the container space
if [ $cleanup_tools -eq 1 ]; then
	apt-get -y --purge remove git 
	apt-get -y --purge remove npm
	apt-get -y clean
	apt-get -y autoremove
fi

# finally give the ownership to username 
chown -R $username:$username $GOTTY_DIR
chmod -R 755 $GOTTY_DIR

#test
if [ $run_tests -eq 1 ]; then
	test_commands=(
		"gcc --version"
		"g++ --version"
		"cling --version"
		"go version"
		"yaegi version"
		"echo 'println(40+2)' | gomacro"
		"python2.7 --version"
		"python3 --version"
		"python --version"
		"ipython3 --version"
		"node --version"
		"npm --version"
		"irb --version"
		"ruby --version"
		"perl --version"
		"perli --version"
		"java --version"
		"jshell --version"
		"gdb --version"
		"jq --version"
		"echo 'puts [info patchlevel]' | tclsh"
		"rustc --version"
		"rust-gdb --version"
		"evcxr --version"
		"sqlite3 --version"
		"tsc --version"
		"ts-node --version"
	)


	# Loop through the array and execute each command, through bash so that a pipe is one.
	# With the PATH of the finished image (what the server and the REPLs have), not this
	# shell's: nvm adds its own folder to this one, which hid that `node` was missing.
	while IFS= read -r cmd; do
	  env PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin bash -c "$cmd"
	  status=$?
	  if [ $status -eq 0 ]; then
	    echo "test [$cmd] => PASSED"
	  else
	    echo "test [$cmd] => FAILED with status code $status"
	    retVal=1
	  fi
	done < <(printf '%s\n' "${test_commands[@]}")

	# Debugging. What the host allows is the host's choice (a builder may have no ptrace),
	# so only the route through qemu has to work; the others are reported.
	echo "test [ptrace on this host] => $(openrepl-ptrace-probe 2>&1)"
	debug_dir=$(mktemp -d)
	cat > "$debug_dir/t.c" <<'EOF'
#include <stdio.h>
int sq(int x) { return x * x; }
int main(void) { printf("%d\n", sq(3)); return 0; }
EOF
	gcc -ggdb -g -static -o "$debug_dir/t.o" "$debug_dir/t.c"
	openrepl-ptrace-probe > /dev/null 2>&1
	has_ptrace=$?
	for route in qemu ptrace; do
	  if [ $route = ptrace ] && [ $has_ptrace -eq 1 ]; then
	    echo "test [debug a program with $route] => SKIPPED (no ptrace here)"
	    continue
	  fi
	  out=$(cd "$debug_dir" && OPENREPL_GDB_ROUTE=$route timeout 120 openrepl-gdb ./t.o -batch -ex 'break sq' -ex run -ex bt -ex delete -ex continue 2>&1)
	  if echo "$out" | grep -q 'Breakpoint 1, sq (x=3)'; then
	    echo "test [debug a program with $route] => PASSED"
	  elif [ $route = qemu ]; then
	    echo "test [debug a program with $route] => FAILED"
	    echo "$out"
	    retVal=1
	  else
	    echo "test [debug a program with $route] => FAILED (not fatal: ptrace is the host's)"
	    echo "$out"
	  fi
	done
	rm -rf "$debug_dir"
	if [ $has_ptrace -eq 1 ]; then
	  echo "test [echo nop | rappel] => SKIPPED (no ptrace here)"
	elif echo 'nop' | timeout 60 rappel > /dev/null 2>&1; then
	  echo "test [echo nop | rappel] => PASSED"
	else
	  echo "test [echo nop | rappel] => FAILED (not fatal)"
	fi
fi

exit $retVal
