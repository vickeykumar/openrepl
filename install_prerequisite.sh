#!/usr/bin/env bash
# please run as sudo
# reboot once after installing to take effect for cgroups and containers

cd ~

apt update -y

# set cap to nsenter,gcc,g++
apt install -y libcap2-bin
setcap "cap_sys_admin,cap_sys_ptrace+ep" /usr/bin/nsenter 
chown root.root /usr/bin/nsenter
chmod 4755 /usr/bin/nsenter
# && ./containers-from-scratch/main run 2 nsenter -n -t$$ /bin/bash
# sudo setcap "cap_sys_admin,cap_sys_ptrace+ep" /usr/bin/arm-linux-gnueabihf-gcc-8
# add export NODE_OPTIONS=--max_old_space_size=2048 to .bashrc
apt install -y net-tools

#install golang
apt install -y golang

#install yaegi go repl, >= go1.18
go install github.com/traefik/yaegi/cmd/yaegi@latest
apt install -y yaegi

#install npm
apt install -y npm
npm install npm@latest -g

#install the last stable release of npm and node for this project using nvm
# node v12.22.9
cd ~
curl -sL https://raw.githubusercontent.com/nvm-sh/nvm/v0.35.0/install.sh -o install_nvm.sh
chmod 755 $HOME/install_nvm.sh
source $HOME/install_nvm.sh
npm install npm@8.5.1 -g
nvm install v12.22.9

#install cling
#use following to compile minimal cling
#/usr/bin/ld.gold --strip-all --no-map-whole-files --no-keep-memory --no-keep-files-mapped $@ 
# cling takes some time to init first instance, add below lines to rc.local(startup)
#/usr/bin/cling 21321 .q > /dev/null 2>&1 &
cd ~
wget https://raw.githubusercontent.com/vickeykumar/openrepl/e596c6f0918e48eeba7a0bf7b7d2632f6b155ffb/repls/cling-Ubuntu-22.04-x86_64-1.0~dev-d47b49c.tar.bz2
tar -xvf cling-Ubuntu-22.04-x86_64-1.0~dev-d47b49c.tar.bz2
chmod 755 cling-Ubuntu-22.04-x86_64-1.0~dev-d47b49c/bin/cling
ln -s cling-Ubuntu-22.04-x86_64-1.0~dev-d47b49c/bin/cling /usr/local/bin/cling

#install gointerpreter
git clone https://github.com/vickeykumar/Go-interpreter.git
cd Go-interpreter
make install
cd ..

#install ipython2.7
apt install -y python2.7
ln -s /usr/bin/python2.7 /usr/bin/python
apt install -y ipython
apt install -y python-is-python3

#install ipython3
apt install -y ipython3

#install Ruby(irb)
apt install -y ruby

#install nodejs
apt install -y nodejs

#install perli
apt install -y rlwrap
# append alias to the /etc/profile
# alias yaegi="rlwrap yaegi"

apt install -y perl
apt install -y perl-doc
git clone https://github.com/vickeykumar/perli.git
cd perli && make install
cd ~

# Docker: Error response from daemon: cgroups: cgroup mountpoint does not exist: unknown
# if using docker use privileged mode with cgroup mounted (-v /sys/fs/cgroup:/sys/fs/cgroup:rw )
mkdir /sys/fs/cgroup/systemd
mount -t cgroup -o none,name=systemd cgroup /sys/fs/cgroup/systemd

apt install -y gdb

