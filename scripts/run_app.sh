#!/bin/bash

# To run REPL containers inside container
mkdir /sys/fs/cgroup/systemd || true
mount -t cgroup -o none,name=systemd cgroup /sys/fs/cgroup/systemd || true

/usr/local/bin/gotty -w --title-format "<fmt><title>{{ .command }}@OpenREPL</title><jid>{{ encodePID .pid }}</jid></fmt>" $@
