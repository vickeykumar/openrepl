FROM ubuntu:22.04 as builder
LABEL maintainer="Vickey Kumar <kumarvickey45@yahoo.com>"

RUN mkdir -p /opt/gotty
RUN mkdir -p /opt/openrepl

WORKDIR /opt/openrepl

ARG DEBIAN_FRONTEND=noninteractive
ENV TZ=Etc/UTC

COPY ./install_prerequisite.sh /opt/openrepl/
RUN ./install_prerequisite.sh

FROM builder as build-image
COPY . /opt/openrepl/
WORKDIR /opt/openrepl/src
RUN make all

FROM build-image
#install the app
RUN cp /opt/openrepl/bin/gotty /usr/local/bin/ && \
mkdir -p /opt/scripts && cp /opt/openrepl/scripts/run_app.sh /opt/scripts/ && \
chmod 755 /usr/local/bin/gotty && chmod 755 /opt/scripts/run_app.sh

WORKDIR /opt/openrepl

RUN mkdir -p /gottyTraces
RUN mkdir -p /opt/gotty

ENV TERM=xterm
ENV GODEBUG=cgocheck=1
ENV GOPATH=/opt/gotty/

ENTRYPOINT ["/opt/scripts/run_app.sh"]
#CMD ["-w", "--title-format", "<fmt><title>{{ .command }}@OpenREPL</title><jid>{{ encodePID .pid }}</jid></fmt>"]
