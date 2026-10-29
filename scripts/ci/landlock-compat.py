#!/usr/bin/env python3
"""Does every REPL and every Run still work under the filesystem sandbox (LLD 14)?

Run inside the image (or any container that has the REPLs and landlock-helper):

    python3 scripts/ci/landlock-compat.py [--helper /usr/local/bin/landlock-helper] \
        [--demos src/resources/meta/demos.xml] [--home-root /home/openrepl]

For each REPL it types a few lines, and for each language of demos.xml it runs the
Compiler script on the sample code, as the server does: first without the sandbox
(the baseline), then under the policy of a workspace. A case passes when the answer
is what the baseline gave. Anything that works without the sandbox and not with it
is a failure of the policy. A case that fails without it too is reported as such and
does not count.

Exit status 0 if nothing that works without the sandbox fails with it.
"""
import argparse
import base64
import os
import shutil
import subprocess
import sys
import tempfile
import xml.etree.ElementTree as ET

# (name, argv, lines typed, text the answer has)
REPLS = [
    ("python", ["python3", "-i"], ["6*7"], "42"),
    ("python2.7", ["python2.7", "-i"], ["6*7"], "42"),
    ("ipython3", ["ipython3", "--simple-prompt"], ["6*7", "exit"], "42"),
    ("node", ["node"], ["40+2", ".exit"], "42"),
    ("ts-node", ["ts-node"], ["40 + 2"], "42"),
    ("gomacro", ["gomacro"], ["x := 6", "x*7"], "42"),
    ("yaegi", ["yaegi"], ["x := 6", "x*7"], "42"),
    ("java", ["jshell"], ["6*7", "/exit"], "42"),
    ("irb", ["irb"], ["6*7", "exit"], "42"),
    ("perli", ["perli"], ["6*7"], "42"),
    ("tclsh", ["tclsh"], ["puts [expr 6*7]"], "42"),
    ("sqlite3", ["sqlite3"], ["select 6*7;", ".quit"], "42"),
    ("cling (C++)", ["cling"], ["int x = 6;", "x*7", ".q"], "42"),
    ("cling (C)", ["cling", "-xc", "-noruntime"], ["#include <stdio.h>", 'printf("%d\\n", 6*7);', ".q"], "42"),
    ("evcxr", ["evcxr"], ["6*7", ":quit"], "42"),
    ("bash", ["bash"], ["echo $((6*7))", "ls /dev/null 2>/dev/null; echo done", "echo hi > f.txt; cat f.txt", "exit"], "42"),
    # a child process reads its own /proc entry, and ps lists processes: a /proc rule for one number breaks them
    ("bash /proc", ["bash"], ["cat /proc/self/status | head -1", "ps -e | head -2", "exit"], "Name:"),
    ("bash ps", ["bash"], ["ps -e | head -2", "exit"], "PID"),
]


def server_env(workspace, name):
    """The environment localcommand.New gives a REPL (without the server's own secrets)."""
    env = {k: v for k, v in os.environ.items() if not k.startswith(("OPENREPL_", "GOTTY_"))}
    env.update({
        "TERM": "xterm", "GOPATH": "/opt/gotty/", "GOCACHE": "/tmp/go_cache/.cache/go-build/",
        "HOME": workspace, "HOSTNAME": name, "GCC_EXEC_PREFIX": "/usr/lib/gcc/",
        "PATH": env.get("PATH", "/usr/local/bin:/usr/bin:/bin") + ":/opt/gotty/bin",
    })
    return env


def run(argv, stdin, env, cwd, timeout):
    try:
        p = subprocess.run(argv, input=stdin, env=env, cwd=cwd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                           universal_newlines=True, timeout=timeout)
        return p.returncode, p.stdout
    except subprocess.TimeoutExpired as e:
        return 124, (e.stdout or "") if isinstance(e.stdout, str) else ""


def sandboxed(helper, workspace, home_root, argv, debug=False):
    # --max-file-mb is what the server passes to every REPL (utils.WorkspaceQuotaMB)
    return [helper, "--workspace", workspace, "--home-root", home_root, "--max-file-mb", "50"] + (["--debug"] if debug else []) + ["--"] + argv


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--helper", default=shutil.which("landlock-helper") or "/usr/local/bin/landlock-helper")
    ap.add_argument("--demos", default=None)
    ap.add_argument("--home-root", default="/home/openrepl")
    ap.add_argument("--only", default=None, help="a name or part of one")
    args = ap.parse_args()

    os.makedirs(args.home_root, exist_ok=True)
    os.chmod(args.home_root, 0o777)
    out = subprocess.run([args.helper, "--probe"], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, universal_newlines=True)
    print("helper:", args.helper, "->", out.stdout.strip())
    if out.returncode != 0:
        print("SKIP: no Landlock on this kernel")
        return 0

    results = []

    def case(name, argv, stdin, expect, timeout=90):
        if args.only and args.only not in name:
            return
        # a workspace each, so that the cases do not meet
        base_ws = tempfile.mkdtemp(prefix="base-", dir=args.home_root)
        box_ws = tempfile.mkdtemp(prefix="box-", dir=args.home_root)
        other_ws = tempfile.mkdtemp(prefix="other-", dir=args.home_root)
        with open(os.path.join(other_ws, "secret.txt"), "w") as f:
            f.write("secret")
        b_rc, b_out = run(argv, stdin, server_env(base_ws, name), base_ws, timeout)
        s_rc, s_out = run(sandboxed(args.helper, box_ws, args.home_root, argv), stdin, server_env(box_ws, name), box_ws, timeout)
        base_ok = expect in b_out
        box_ok = expect in s_out
        if base_ok and box_ok:
            status = "PASS"
        elif not base_ok:
            status = "BASELINE-FAILS"  # not the sandbox's doing
        else:
            status = "FAIL"
        results.append((name, status))
        print("%-16s %s" % (name, status))
        if status == "FAIL":
            print("    without the sandbox: rc=%s, %r" % (b_rc, b_out[-300:]))
            print("    with the sandbox   : rc=%s, %r" % (s_rc, s_out[-600:]))
        elif status == "BASELINE-FAILS":
            print("    (without the sandbox too: rc=%s, %r)" % (b_rc, b_out[-200:]))
        for d in (base_ws, box_ws, other_ws):
            shutil.rmtree(d, ignore_errors=True)

    for name, argv, lines, expect in REPLS:
        case("repl " + name, argv, "\n".join(lines) + "\n", expect, 120 if name == "java" else 60)

    # the Run button: the Compiler script of each language on its sample code
    if args.demos:
        for demo in ET.parse(args.demos).getroot().iter("Demo"):
            script, content = demo.findtext("Compiler"), demo.findtext("Content")
            name = demo.findtext("Name")
            if not script or not content:
                continue
            payload = base64.b64encode(content.encode()).decode()
            argv = ["/bin/bash", "-c", script, payload, ""]
            # what a sample prints is the baseline: compare with it, not with a fixed text
            base_ws = tempfile.mkdtemp(prefix="base-", dir=args.home_root)
            b_rc, b_out = run(argv, "", dict(server_env(base_ws, name), IdeLang=name, CompilerOption="", IdeFileName=""), base_ws, 180)
            shutil.rmtree(base_ws, ignore_errors=True)
            marker = [l for l in b_out.splitlines() if l.strip()][-1:] or [""]
            if args.only and args.only not in "run " + name:
                continue
            box_ws = tempfile.mkdtemp(prefix="box-", dir=args.home_root)
            s_rc, s_out = run(sandboxed(args.helper, box_ws, args.home_root, argv), "",
                              dict(server_env(box_ws, name), IdeLang=name, CompilerOption="", IdeFileName=""), box_ws, 180)
            shutil.rmtree(box_ws, ignore_errors=True)
            ok_base = b_rc == 0 and len(b_out.strip()) > 0
            same = marker[0] and marker[0] in s_out
            status = "PASS" if ok_base and same else ("BASELINE-FAILS" if not ok_base else "FAIL")
            results.append(("run " + name, status))
            print("%-16s %s" % ("run " + name, status))
            if status == "FAIL":
                print("    without the sandbox: rc=%s, %r" % (b_rc, b_out[-300:]))
                print("    with the sandbox   : rc=%s, %r" % (s_rc, s_out[-600:]))
            elif status == "BASELINE-FAILS":
                print("    (without the sandbox too: rc=%s, %r)" % (b_rc, b_out[-200:]))

    # Debug: gdb writes the memory of the program it debugs through /proc/<pid>/mem, which a Debug run may do.
    # "reached main" is not enough: the breakpoint must be inserted and the program must run to its end.
    if args.demos and (not args.only or args.only in "debug"):
        demo = next((d for d in ET.parse(args.demos).getroot().iter("Demo") if d.findtext("Name") == "cling"), None)
        if demo is not None:
            payload = base64.b64encode(demo.findtext("Content").encode()).decode()
            argv = ["/bin/bash", "-c", demo.findtext("Compiler"), payload, ""]
            typed = "break main\nrun\nbt\nnext\ninfo locals\ncontinue\nquit\n"
            outs = []
            for sandbox in (False, True):
                ws = tempfile.mkdtemp(prefix="dbg-", dir=args.home_root)
                os.chmod(ws, 0o777)
                env = dict(server_env(ws, "cling"), IdeLang="c", CompilerOption="debug", IdeFileName="")
                cmd = sandboxed(args.helper, ws, args.home_root, argv, debug=True) if sandbox else argv
                rc, out = run(cmd, typed, env, ws, 180)
                outs.append(out)
                shutil.rmtree(ws, ignore_errors=True)
            base_ok = "Breakpoint 1, main" in outs[0] and "Cannot insert breakpoint" not in outs[0]
            box_ok = "Breakpoint 1, main" in outs[1] and "Cannot insert breakpoint" not in outs[1] and "exited normally" in outs[1]
            status = "PASS" if base_ok and box_ok else ("BASELINE-FAILS" if not base_ok else "FAIL")
            results.append(("debug c", status))
            print("%-16s %s" % ("debug c", status))
            if status == "FAIL":
                print("    with the sandbox   : %r" % outs[1][-500:])
            elif status == "BASELINE-FAILS":
                print("    (without the sandbox too: %r)" % outs[0][-300:])

    # the per-file limit (RLIMIT_FSIZE): a REPL that writes a file bigger than the limit gets an error
    ws = tempfile.mkdtemp(prefix="box-", dir=args.home_root)
    os.chmod(ws, 0o777)
    big = ["python3", "-c", "open('big.bin', 'wb').write(b'x' * 3000000); print('WROTE')"]
    small = ["python3", "-c", "open('small.bin', 'wb').write(b'x' * 100000); print('WROTE')"]
    limited = [args.helper, "--workspace", ws, "--home-root", args.home_root, "--max-file-mb", "1", "--"]
    rc_big, o_big = run(limited + big, "", server_env(ws, "python"), ws, 30)
    rc_small, o_small = run(limited + small, "", server_env(ws, "python"), ws, 30)
    ok = "File too large" in o_big and "WROTE" not in o_big and "WROTE" in o_small
    results.append(("file size limit", "PASS" if ok else "FAIL"))
    print("%-16s %s" % ("file size limit", "PASS" if ok else "FAIL big=%r small=%r" % (o_big[-120:], o_small[-120:])))
    shutil.rmtree(ws, ignore_errors=True)

    # the isolation itself, with a real REPL: bash under the policy tries what it must not do
    box_ws = tempfile.mkdtemp(prefix="box-", dir=args.home_root)
    other_ws = tempfile.mkdtemp(prefix="other-", dir=args.home_root)
    with open(os.path.join(other_ws, "secret.txt"), "w") as f:
        f.write("secret")
    probe = (
        'cat "%s/secret.txt" >/dev/null 2>&1 && echo LEAK-READ; '
        'echo x > "%s/pwned.txt" 2>/dev/null && echo LEAK-WRITE; '
        'ls "%s" >/dev/null 2>&1 && echo LEAK-LIST; '
        'echo x > mine.txt && echo own-ok; echo done'
    ) % (other_ws, other_ws, args.home_root)
    rc, o = run(sandboxed(args.helper, box_ws, args.home_root, ["/bin/bash", "-c", probe]), "", server_env(box_ws, "bash"), box_ws, 30)
    leaked = [w for w in ("LEAK-READ", "LEAK-WRITE", "LEAK-LIST") if w in o]
    ok = not leaked and "own-ok" in o
    results.append(("isolation", "PASS" if ok else "FAIL"))
    print("%-16s %s" % ("isolation", "PASS" if ok else "FAIL " + str(leaked) + " " + o[-200:]))
    shutil.rmtree(box_ws, ignore_errors=True)
    shutil.rmtree(other_ws, ignore_errors=True)

    failed = [n for n, s in results if s == "FAIL"]
    print("\n%d passed, %d failed, %d fail without the sandbox too" % (
        sum(1 for _, s in results if s == "PASS"), len(failed), sum(1 for _, s in results if s == "BASELINE-FAILS")))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
