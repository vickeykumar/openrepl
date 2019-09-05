#!/usr/bin/env bash
#please run as root/sudo

cd ~

#install golang
apt install golang

#install npm
apt install npm
npm install npm@latest -g

#install cling

#install gointerpreter
git clone https://github.com/vickeykumar/Go-interpreter.git
cd Go-interpreter
make install
cd ..

#install ipython2.7
apt install ipython

#install ipython3
apt install ipython3

#install Ruby(irb)
apt install ruby

#install nodejs
apt install nodejs

#install perli
apt install rlwrap
apt install perl
apt install perl-doc
git clone https://github.com/vickeykumar/perli.git
make install



