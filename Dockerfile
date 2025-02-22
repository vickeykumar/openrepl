FROM ubuntu:22.04 as builder
LABEL maintainer="Vickey Kumar <kumarvickey45@yahoo.com>"

RUN mkdir -p /opt/gotty &&  mkdir -p /opt/openrepl

WORKDIR /opt/openrepl

#ARG DEBIAN_FRONTEND=noninteractive
ENV TZ=Etc/UTC
# place timezone in /etc/timezone
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

COPY ./install_prerequisite.sh /opt/openrepl/
COPY ./bin/gdb /usr/bin/
RUN ./install_prerequisite.sh --cleanup-tools --run-tests

FROM builder as build-image

#install the requisites to build the APP
RUN apt-get install -y --no-install-recommends make gcc git npm && npm install -g npm@8.5.1
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
