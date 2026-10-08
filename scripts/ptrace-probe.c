/* Can a debugger work on this host?
 *
 * gdb needs two things from the kernel: to trace a child (PTRACE_TRACEME) and to
 * read its registers (PTRACE_GETREGS). Some hosts allow neither (a kernel or a
 * sandbox without ptrace: "Function not implemented"), some only the first (x86
 * code run through Rosetta: "Input/output error"). This does exactly those two
 * things and says which one failed.
 *
 *   exit 0  ptrace works (prints "ok")
 *   exit 2  ptrace works, but the address space randomization cannot be switched
 *           off, so gdb prints a warning (prints "ok-aslr")
 *   exit 1  ptrace does not work (prints why)
 *
 * It is built once, with the image (install_prerequisite.sh), and installed as
 * /usr/local/bin/openrepl-ptrace-probe; openrepl-gdb runs it on every Debug.
 */
#include <errno.h>
#include <signal.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
#include <sys/personality.h>
#include <sys/ptrace.h>
#include <sys/types.h>
#include <sys/user.h>
#include <sys/wait.h>

int main(void) {
    pid_t pid = fork();
    if (pid < 0) {
        printf("fork: %s\n", strerror(errno));
        return 1;
    }
    if (pid == 0) {
        if (ptrace(PTRACE_TRACEME, 0, 0, 0) != 0) _exit(errno == ENOSYS ? 101 : 100);
        raise(SIGSTOP);
        _exit(0);
    }

    int st = 0;
    if (waitpid(pid, &st, 0) < 0) {
        printf("waitpid: %s\n", strerror(errno));
        return 1;
    }
    if (WIFEXITED(st)) {
        printf("traceme: %s\n", WEXITSTATUS(st) == 101 ? "not implemented" : "not permitted");
        return 1;
    }

    struct user_regs_struct regs;
    int regs_ok = ptrace(PTRACE_GETREGS, pid, 0, &regs) == 0;
    int why = errno;
    kill(pid, SIGKILL);
    waitpid(pid, &st, 0);
    if (!regs_ok) {
        printf("getregs: %s\n", strerror(why));
        return 1;
    }

    int current = personality(0xffffffff);
    if (current == -1 || personality(current | ADDR_NO_RANDOMIZE) == -1) {
        printf("ok-aslr\n");
        return 2;
    }
    printf("ok\n");
    return 0;
}
