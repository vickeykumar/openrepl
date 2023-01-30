FROM ubuntu:22.04 as builder
LABEL maintainer="Vickey Kumar <kumarvickey45@yahoo.com>"

RUN mkdir -p /opt/gotty &&  mkdir -p /opt/openrepl

WORKDIR /opt/openrepl

ARG DEBIAN_FRONTEND=noninteractive
ENV TZ=Etc/UTC

COPY ./install_prerequisite.sh /opt/openrepl/
RUN ./install_prerequisite.sh

FROM builder as build-image
COPY . /opt/openrepl/
WORKDIR /opt/openrepl/src
RUN make all

FROM builder
#install the app

RUN mkdir -p /gottyTraces && mkdir -p /opt/gotty && \
mkdir -p /opt/scripts

COPY --from=build-image /opt/openrepl/bin/gotty /usr/local/bin/
COPY --from=build-image /opt/openrepl/scripts/run_app.sh /opt/scripts/

RUN chmod 755 /usr/local/bin/gotty && chmod 755 /opt/scripts/run_app.sh

WORKDIR /opt/openrepl

ENV TERM=xterm
ENV GODEBUG=cgocheck=1
ENV GOPATH=/opt/gotty/

EXPOSE 80
ENTRYPOINT ["/opt/scripts/run_app.sh"]
CMD ["-p", "80"]
