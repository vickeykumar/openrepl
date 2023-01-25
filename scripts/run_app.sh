#!/bin/bash

mkdir /sys/fs/cgroup/systemd
mount -t cgroup -o none,name=systemd cgroup /sys/fs/cgroup/systemd || true

/usr/local/bin/gotty $@
