#!/bin/bash

# Run as PID 1 so the cgroup delegated to this container has no leftover shell
# process when gotty enables the cgroup v2 memory controller.
exec /usr/local/bin/gotty -w --title-format "<fmt><title>{{ .command }}</title><jid>{{ encodePID .pid }}</jid></fmt>" "$@"
