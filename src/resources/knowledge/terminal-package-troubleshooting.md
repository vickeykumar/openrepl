---
title: Check package tools and downloads in Bash
keywords: check node version, npm version, java version, maven version, check perl LWP, LWP::UserAgent OK, test node proxy, npm view, cargo cache scratch, CARGO_HOME, CARGO_INSTALL_ROOT, PCRE2 unavailable, Maven source option 5, Java 21 Maven build error
link: /doc.html#installing-apps
---
To check the image, run `node --version`, `npm --version`, `java --version`, `mvn --version`, and `perl -MLWP::UserAgent -e 'print "LWP::UserAgent OK\n"'`. Versions depend on the deployed image.

With site egress enabled, `NODE_USE_ENV_PROXY=1` makes Node's HTTPS client use the configured proxy; a 200 response and a successful `npm view lodash version` confirm proxy-backed access, not direct network access. Maven `dependency:get` downloads a jar but does not set `CLASSPATH`. If an old Maven quickstart fails on Java 21 with source/target 5, build that project with compiler source and target 8. Cargo registry and build caches use Scratch; `CARGO_INSTALL_ROOT` holds installed commands in the home directory. A PCRE2-unavailable message is optional if `rg --version` succeeds.
