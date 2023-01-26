FROM ubuntu:22.04
LABEL maintainer="Vickey Kumar <kumarvickey45@yahoo.com>"

RUN mkdir -p /opt/gotty
RUN mkdir -p /opt/openrepl

COPY . /opt/openrepl/

RUN apt-get -y update && apt-get install -y sudo

WORKDIR /opt/openrepl

ARG DEBIAN_FRONTEND=noninteractive
ENV TZ=Etc/UTC

RUN sudo ./install_prerequisite.sh

WORKDIR /opt/openrepl/src

RUN make all

WORKDIR /opt/openrepl

#install the app
RUN cp /opt/openrepl/bin/gotty /usr/local/bin/
RUN mkdir -p /opt/scripts && cp /opt/openrepl/scripts/run_app.sh /opt/scripts/ && \
chmod 755 /usr/local/bin/gotty && chmod 755 /opt/scripts/run_app.sh

ENV TERM=xterm
ENV GODEBUG=cgocheck=1

RUN mkdir -p /gottyTraces
RUN mkdir -p /opt/gotty
ENV GOPATH=/opt/gotty/

ENTRYPOINT ["/opt/scripts/run_app.sh"]
#CMD ["-w", "--title-format", "<fmt><title>{{ .command }}@OpenREPL</title><jid>{{ encodePID .pid }}</jid></fmt>"]
