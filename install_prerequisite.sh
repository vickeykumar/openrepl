#!/usr/bin/env bash
# please run as sudo
# reboot once after installing to take effect for cgroups and containers

GOTTY_DIR=/opt/gotty 
mkdir -p $GOTTY_DIR || true
chmod 644 $GOTTY_DIR
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

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
nvm install v12.22.9
npm install npm@8.5.1 -g

#install cling
#use following to compile minimal cling
#/usr/bin/ld.gold --strip-all --no-map-whole-files --no-keep-memory --no-keep-files-mapped $@ 
# cling takes some time to init first instance, add below lines to rc.local(startup)
#/usr/bin/cling 21321 .q > /dev/null 2>&1 &
cd $GOTTY_DIR

if [ -e "/usr/local/bin/cling" ]; then
    echo "File /usr/local/bin/cling exists."
else
    echo "File /usr/local/bin/cling does not exist. installing..."
    wget https://raw.githubusercontent.com/vickeykumar/openrepl/e596c6f0918e48eeba7a0bf7b7d2632f6b155ffb/repls/cling-Ubuntu-22.04-x86_64-1.0~dev-d47b49c.tar.bz2
		tar -xvf cling-Ubuntu-22.04-x86_64-1.0~dev-d47b49c.tar.bz2
		chmod 755 cling-Ubuntu-22.04-x86_64-1.0~dev-d47b49c/bin/cling
		ln -s $GOTTY_DIR/cling-Ubuntu-22.04-x86_64-1.0~dev-d47b49c/bin/cling /usr/local/bin/cling
		chmod 755 /usr/local/bin/cling
		rm cling-Ubuntu-22.04-x86_64-1.0~dev-d47b49c.tar.bz2
fi

#install evcxr and set all the required dependencies
if [ -e "/usr/local/bin/evcxr" ]; then
    echo "File /usr/local/bin/evcxr exists."
else
    echo "File /usr/local/bin/evcxr does not exist. installing..."
    apt-get install -y --no-install-recommends rustc
    apt-get install -y --no-install-recommends rust-gdb
    apt-get install -y --no-install-recommends cargo
    #evcxr install
    wget https://github.com/evcxr/evcxr/releases/download/v0.17.0/evcxr-v0.17.0-x86_64-unknown-linux-gnu.tar.gz
    tar -xvf evcxr-v0.17.0-x86_64-unknown-linux-gnu.tar.gz
    cp evcxr-v0.17.0-x86_64-unknown-linux-gnu/evcxr /usr/local/bin/evcxr
    chmod 755 /usr/local/bin/evcxr
    rm evcxr-v0.17.0-x86_64-unknown-linux-gnu.tar.gz
    rm -rf evcxr-v0.17.0-x86_64-unknown-linux-gnu
fi



#install gointerpreter
git clone https://github.com/vickeykumar/Go-interpreter.git
cd Go-interpreter
make install
cd ..

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
npm install -g ts-node
npm link typescript

# Docker: Error response from daemon: cgroups: cgroup mountpoint does not exist: unknown
# if using docker use privileged mode with cgroup mounted (-v /sys/fs/cgroup:/sys/fs/cgroup:rw )
mkdir /sys/fs/cgroup/systemd || true
mount -t cgroup -o none,name=systemd cgroup /sys/fs/cgroup/systemd 2>&1 || true

# check /sys/fs/cgroup/memory is mounted correctly, if not
# enable memory cgroup V1 by changing /etc/default/grub
# GRUB_CMDLINE_LINUX="systemd.unified_cgroup_hierarchy=0"
# cgroup_enable=memory
# reboot


# currently stable gdb version is 8.1.1, can be copied from bin/ build from src
#apt-get install -y --no-install-recommends gdb
cp $SCRIPT_DIR/bin/gdb /usr/local/bin/ || true
chmod 755 /usr/local/bin/gdb || true
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
		"gdb --version"
		"jq --version"
		"echo 'puts [info patchlevel]' | tclsh"
		"rustc --version"
		"rust-gdb --version"
		"evcxr --version"
		"sqlite3 --version"
	)


	# Loop through the array and execute each command
	for cmd in "${test_commands[@]}"; do
	  $cmd
	  if [ $? -eq 0 ]; then
	    echo "test [$cmd] => PASSED"
	  else
	    echo "test [$cmd] => FAILED with status code $?"
	    retVal=1
	  fi
	done
fi

exit $retVal
