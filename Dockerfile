FROM ubuntu:22.04
LABEL maintainer="Vickey Kumar <kumarvickey45@yahoo.com>"

RUN mkdir -p /opt/gotty

WORKDIR /opt

RUN apt-get -y update
RUN apt-get -y install git
RUN apt install -y sudo
RUN git clone https://github.com/vickeykumar/openrepl.git

WORKDIR /opt/openrepl

ARG DEBIAN_FRONTEND=noninteractive
ENV TZ=Etc/UTC

RUN sudo ./install_prerequisite.sh

WORKDIR /opt/openrepl/src

RUN make all

WORKDIR /opt/openrepl

ENV TERM=xterm
ENV GODEBUG=cgocheck=1

RUN mkdir -p /gottyTraces
RUN mkdir -p /opt/gotty
ENV GOPATH=/opt/gotty/

ENTRYPOINT ["/opt/openrepl/bin/gotty"]
CMD "-w --title-format \"<fmt><title>{{ .command }}@OpenREPL</title><jid>{{ encodePID .pid }}</jid></fmt>\""
