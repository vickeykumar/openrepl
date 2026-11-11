# 16. Network emulation (a private network per workspace, without privilege)

**Status: implemented** (`src/netemu`, the helper's `--net-emulation`, `containers/netemu.go`). Setting `OPENREPL_NET_ISOLATION=auto|off|require`.

## Why

A REPL that runs in a kernel network namespace (the cgroup sandbox on a privileged host such as the Raspberry Pi workers, LLD 03) has a private loopback: every user may listen on port 8080, and nobody reaches another user's server or the outside. A host that refuses privileged containers (Render) cannot make that namespace: unprivileged user namespaces are refused by the host (`apparmor_restrict_unprivileged_userns`), and there is no `CAP_SYS_ADMIN`. There every REPL shared one network: a port could be used by one user only, and users could reach each other's servers and the internet.

This layer gives such a REPL the same behaviour, in user space, with no privilege.

## How it works

Each `AF_INET`/`AF_INET6` socket of the REPL is really an **`AF_UNIX` socket bound to an abstract name** that holds the workspace and the port: `@openrepl-ne/<workspace id>/<t|u>/<port>` (`t` stream, `u` datagram; the workspace id is a short hash of the workspace folder, `netemu.WorkspaceID`). The kernel does what it is good at for such sockets: `listen`, `accept`, `epoll`, buffers, half-close, "connection refused" (nobody is bound to the name), "address in use" (the name is taken). Data is not touched.

A supervisor in the server answers only the system calls that carry an address. The REPL can name only its own workspace's ports because the **supervisor writes the prefix**; only the local machine exists (`0.0.0.0`, `127.x.x.x`, `::`, `::1`, `::ffff:127.x.x.x`), anything else is `ENETUNREACH`.

| Call | What the supervisor does |
|---|---|
| `socket` | IP stream or datagram: makes the unix socket and injects it into the REPL (`SECCOMP_IOCTL_NOTIF_ADDFD` with `SEND`). `AF_UNIX` stream: allowed as it is. `AF_UNIX` datagram, raw, packet, netlink, ICMP: refused. |
| `bind` | Binds the name (port 0: a random ephemeral port in 32768-60999, the range Linux uses). Remembers the family and whether it was the wildcard. |
| `connect` | Binds an ephemeral name if the socket has none (so the server sees a peer address), then connects to the name. A full backlog is waited for (a TCP connect would queue). |
| `getsockname`, `getpeername` | Reads the name back and gives the REPL the `sockaddr_in`/`sockaddr_in6` it expects (`127.0.0.1`, `::1`, the peer of an IPv6 socket as IPv4-mapped). |
| `accept`, `accept4` with an address | Accepts, writes the peer address, injects the connection. Without an address the kernel does it itself. |
| `sendto`, `recvfrom` with an address, `sendmsg`, `recvmsg` on datagram sockets | Translates the address; the supervisor copies the payload (a datagram with an explicit address costs a round trip). `sendmmsg`/`recvmmsg` on a datagram socket answer `ENOSYS` (libuv and others fall back to `sendmsg`/`recvmsg`). |
| `setsockopt`, `getsockopt` above the socket level | Accepted and ignored (TCP, IP, IPv6 options mean nothing here). Socket-level options go to the kernel. |
| `AF_UNIX` addresses | Never carried out: `bind`, `connect` or a send to an `AF_UNIX` address is `EACCES`. This is what keeps a REPL from naming another workspace's abstract socket by hand. A `socketpair` is not touched. |

Everything else (`read`, `write`, `epoll`, `shutdown`, `listen`, `dup`, `fork`) goes straight to the kernel: the filter notifies only where an address is involved (calls with no address pointer are allowed without a round trip, `netemu/filter.go`). The `io_uring` calls are refused (`ENOSYS`), because they can make a socket call that does not pass the filter; calls from another architecture (32-bit) kill the process.

The supervisor never lets the kernel carry out a call whose address it has not copied and checked itself (no `SECCOMP_USER_NOTIF_FLAG_CONTINUE` for those): the REPL could change the memory between the check and the call.

## Where it runs

1. **The helper** (`landlock-helper`, `src/landlockhelper`) is the process that becomes the REPL. With `--net-emulation` it loads the seccomp filter (`netemu.Install`) after the Landlock policy (which already sets `no_new_privs`) and before the `exec`. If that fails the REPL does not start: there is no unrestricted fallback. `--no-landlock --net-emulation` is the same without the filesystem policy.
2. **The server** gives the helper a socket as descriptor 3 (`localcommand.New`, `ExtraFiles`). The helper tells it the number of its notification descriptor; the server copies it with `pidfd_getfd` and acknowledges (the helper cannot pass a descriptor with `sendmsg`: the filter already sends that call to the server). Then `netemu.Attach` serves the session in the background until the REPL and everything it started are gone (`POLLHUP` on the notification descriptor).
3. **Workspace, not process.** All terminals and forks of a workspace get the same name prefix, so they share one network: a server in one terminal is reachable from a client in another (or in a fork). Another workspace has other names.

When it applies: a non-admin REPL that has no kernel namespaces of its own (`ownNamespaces` false: the same condition as the signal scope of LLD 14). An admin's REPL is not wrapped at all. A REPL with a kernel network namespace (the Pi) keeps it and is not touched.

## Start-up probe and the mode

`containers.InitNetEmu` (after `InitLandlock`) runs the helper's `--net-probe` under a real supervisor: the helper installs the filter and runs a TCP exchange, a UDP exchange and a connect to `192.0.2.1` that must fail. Having the kernel features is not enough: a host can refuse the permission checks (Docker's default seccomp profile blocks `pidfd_getfd`; a Yama `ptrace_scope` that excludes the server).

| `OPENREPL_NET_ISOLATION` | |
|---|---|
| `auto` (default) | on where the probe passes; otherwise a warning in the log and on the admin Health page ("Network isolation"), and REPLs run as before |
| `off` | never |
| `require` | a non-admin REPL does not start unless the emulation is active |

## What it needs from the host

Linux 5.9 or later (seccomp user notification with `ADDFD`), `pidfd_open`/`pidfd_getfd` allowed, and ptrace-style access from the server to the REPL's processes. With Yama `ptrace_scope=1` that holds for **descendants** of the server: a program that daemonises is adopted by PID 1, which is `gotty` in the Docker image (and `reaper` cleans what ends), so it stays a descendant. If PID 1 were another program, the server would have to become a child subreaper. Checked on Render (kernel 7.0, `ptrace_scope` 1): a child passes, an orphan re-parented to a different PID 1 does not.

## What a user sees

- The same port for everyone; `127.0.0.1` and `::1` work; a port nobody listens on is "connection refused"; every other address is "network is unreachable" (for non-admins on such a host there is no outside network, as on the Pi). So `pip install`, `curl`, `git clone` do not work there.
- `getsockname` and `getpeername` show real-looking addresses.
- **Not the same as a kernel namespace:** `ss`, `netstat` and `lsof -i` do not show these sockets (they are unix sockets); `ifconfig`/`ip` show the host's interfaces (not hidden); `/proc/net` lists the host's sockets; named unix sockets (`AF_UNIX` paths) are refused; the peer of a connection from an IPv4 client to an IPv6 listener is shown IPv4-mapped; `IPV6_V6ONLY` is not honoured (IPv4 and IPv6 share one port space, as with the Linux default); raw sockets and `ping` do not work; a REPL that makes thousands of connections per second is slower (a connect costs about 0.4 ms instead of 0.02 ms; stream throughput about half of TCP over loopback in the spike).
- It is a **policy enforced by the supervisor**, not a kernel boundary like a namespace: for strong isolation run guest REPLs on a privileged worker.

## Robustness notes (found while building it)

- The notification names a **thread**: `pidfd_open` needs the process (`tgid`).
- A signal can cancel a notification while it is being answered. `ADDFD` then fails with `ENOENT` or `EINPROGRESS`: nothing is sent, and a connection accepted for that call goes to a per-listener stash that the retried `accept` returns. `ENOENT` from a receive is not the end of the session (the end is `POLLHUP`).
- Go's `syscall` wrappers cannot read an abstract name back; the package uses raw system calls.
- Tests: `netemu` unit tests (address parsing, names, the filter program run through a small BPF interpreter) and Linux integration tests that run the REPL side in a copy of the test binary (two workspaces on the same ports for TCP, UDP and IPv6; terminals of one workspace sharing a network and another workspace refused; unix names and raw sockets refused; 1500 rapid connections). They **skip** on a host where the emulation does not work, like the Landlock tests.

## Not done

`sendmmsg`/`recvmmsg` on datagram sockets, ancillary data (`SCM_RIGHTS`) on emulated datagram sockets, `SO_REUSEPORT` load balancing, hiding the host's interfaces from `ifconfig`/`ip`, an allow-list for outbound connections, honouring `IPV6_V6ONLY`.
