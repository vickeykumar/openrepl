---
title: Ports, sockets and the network in a REPL
keywords: port, ports, socket, sockets, tcp, udp, server, client, localhost, 127.0.0.1, address already in use, same port, 8080, network, internet, no internet, pip install, curl, ipv6, bind, listen, connect, connection refused, network is unreachable, ss, netstat
link: /about.html
---
On OpenREPL you can write socket programs (TCP and UDP) and use any port, for example 8080. Your REPL has a private network that only your own terminals share: a server in one terminal can be reached from a client in another terminal (or in a fork) at 127.0.0.1 or localhost, and other users can use the same ports at the same time without clashing. Nobody else can reach your server, and you cannot reach theirs. A port nobody listens on gives "connection refused".

Programs cannot reach the internet or any address outside your REPL (they get "network is unreachable"), so pip install, curl to a website and git clone do not work from your REPL. On some servers this is a private network of the kernel, on others it is emulated; programs see the same, except that tools which list sockets (ss, netstat) may not show them, ping and raw sockets do not work, and named Unix sockets (file paths) are not available.
