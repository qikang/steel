// Package shell implements the `shell` module: SSH into a host,
// run a command under a login shell, and stream the output back.
//
// Run is the only entry point. It is responsible for:
//   - resolving the user command (and optional `chdir:`) against
//     the vars + host context via vars.Render
//   - building the env prelude (os_arch, all_node_hostname, the
//     per-host <name>_ip / <name>_hostname pairs, the playbook
//     vars flattened to underscore-separated names) and
//     prepending it to the user's command
//   - opening an SSH session via the shared connect helper
//   - pumping stdout/stderr through line-buffered goroutines so a
//     logical line split across reads still renders as one prefixed
//     output line
//   - recording the remote exit code on the returned Result
//
// The non-trivial helpers (prelude, pump, quoting) live in
// sibling files in this package — quote.go, pump.go, prelude.go
// — so shell.go itself stays focused on the SSH/PTY flow.
package shell

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/ssh"

	"steel/internal/inventory"
	"steel/internal/playbook"
	"steel/internal/runner/arch"
	"steel/internal/runner/connect"
	"steel/internal/runner/logstream"
	"steel/internal/vars"
)

// Run executes the spec. See the package-level doc for the
// supported optional arguments: log, when non-nil, mirrors the
// remote stdout/stderr into the per-host log file; allHosts is
// the full inventory, used by the prelude builder to emit the
// per-host `<hostName>_ip` / `<hostName>_hostname` env vars per
// the install-k8s.yaml spec.
func Run(h *inventory.Host, spec *playbook.ShellSpec, ctx *vars.Context, log *logstream.Streamer, allHosts []*inventory.Host) Result {
	cmd, err := vars.Render(spec.Cmd, ctx)
	if err != nil {
		return Result{Host: h.Name, Err: err}
	}
	// chdir is rendered through the same template engine as cmd
	// so values like `{{ data_root }}/common/foo` resolve per
	// host. Wrapping the user's command in `cd <dir> && ...` is
	// the ansible-style behavior the playbooks assume: the chdir
	// happens inside the login shell (so $HOME/profile/aliases
	// are already loaded) and any failure to enter the directory
	// surfaces as the command's own non-zero exit — no special-
	// case error path.
	if spec.Chdir != "" {
		dir, err := vars.Render(spec.Chdir, ctx)
		if err != nil {
			return Result{Host: h.Name, Err: fmt.Errorf("render chdir: %w", err)}
		}
		cmd = applyChdir(cmd, dir)
	}
	// Inject per-host env vars ahead of the user's command. The
	// collected set covers:
	//   - os_arch (probe result, used by installer scripts that
	//     pick amd64 vs arm64 binaries by $os_arch in the
	//     URL/path);
	//   - one <hostName>_ip and one <hostName>_hostname pair per
	//     host in the inventory, so a script on master1 can
	//     reference $master2_ip without a separate lookup;
	//   - every top-level var, with nested fields flattened to
	//     underscore-separated names.
	// We prepend AFTER applyChdir so the export sits ahead of
	// `cd ... && ...`; the `&&` chain still short-circuits on cd
	// failure (matching ansible semantics), and any subshell the
	// user's command spawns inherits everything because bash
	// exports them with `export`. arch.Of is cached on host.Name,
	// so the os_arch probe is free on subsequent plays.
	archName, err := arch.Of(h)
	if err != nil {
		return Result{Host: h.Name, Err: err}
	}
	prelude := buildEnvPrelude(archName, ctx, allHosts)
	if prelude != "" {
		// Join prelude and the user's command with `; `, not a
		// bare space. A space leaves the last `export …=…` of
		// the prelude in the same statement as the user's first
		// token, so bash parses the user's first word as an
		// extra argument to `export` (which then errors out on a
		// value like `cluster.local` because `export` rejects
		// non-identifier args that aren't NAME=VALUE).
		cmd = prelude + "; " + cmd
	}
	bash := spec.Bash
	if bash == "" {
		bash = "/bin/bash"
	}
	// `-l` = login shell, `-c` = take the command from the next
	// arg. We pass cmd as a single argument so embedded quotes /
	// newlines survive.
	remoteCmd := fmt.Sprintf("%s -l -c %s", shellQuote(bash), shellQuote(cmd))

	conn, err := connect.Connect(h)
	if err != nil {
		return Result{Host: h.Name, Err: err}
	}
	defer conn.Close()

	sess, err := conn.Client.NewSession()
	if err != nil {
		return Result{Host: h.Name, Err: fmt.Errorf("new session: %w", err)}
	}
	defer sess.Close()

	stdoutR, stdoutW := io.Pipe()
	stderrR, stderrW := io.Pipe()
	sess.Stdout = stdoutW
	sess.Stderr = stderrW

	// Request a PTY so the remote side behaves like an
	// interactive login shell — close to what the user would see
	// over plain SSH.
	modes := ssh.TerminalModes{ssh.ECHO: 0, ssh.TTY_OP_ISPEED: 14400, ssh.TTY_OP_OSPEED: 14400}
	if err := sess.RequestPty("xterm", 40, 200, modes); err != nil {
		// PTY can be denied on hardened images; non-fatal — the
		// command still runs.
		_ = err
	}

	// Run in a goroutine so we can pump stdout/stderr concurrently
	// and still observe the exit code on this side.
	runErrCh := make(chan error, 1)
	go func() {
		// Closing the write side of each pipe after Run() returns
		// lets the pump goroutines drain and exit cleanly.
		defer stdoutW.Close()
		defer stderrW.Close()
		runErrCh <- sess.Run(remoteCmd)
	}()

	// Two pump goroutines — one per stream — write to the
	// run-wide log file and a buffer (the latter for programmatic
	// callers). The terminal sink is io.Discard: the console is a
	// "task progress" view, not a live stream; all detailed
	// stdout/stderr lives in the single run-wide log file under
	// logs/<run-ts>/run.log.
	var stdoutBuf, stderrBuf strings.Builder
	done := make(chan struct{}, 2)
	go startPump(stdoutR, io.Discard, log, h.Name, "stdout", &stdoutBuf, nil, done)
	go startPump(stderrR, io.Discard, log, h.Name, "stderr", &stderrBuf, nil, done)
	<-done
	<-done

	runErr := <-runErrCh
	res := Result{
		Host:   h.Name,
		Stdout: stdoutBuf.String(),
		Stderr: stderrBuf.String(),
	}
	if runErr != nil {
		res.OK = false
		// Surface the remote exit code in the error so the log
		// footer and the terminal failure line both show the
		// actual number (e.g. "status 7") rather than a generic
		// non-zero hint.
		if ee, ok := runErr.(*ssh.ExitError); ok {
			res.ExitCode = ee.ExitStatus()
			// ssh.ExitError.Msg is a method, not a field;
			// "status N" is the useful payload.
			res.Err = fmt.Errorf("remote %s: process exited with status %d", h.Name, ee.ExitStatus())
		} else {
			res.ExitCode = -1
			res.Err = fmt.Errorf("remote %s: %w", h.Name, runErr)
		}
		return res
	}
	res.OK = true
	res.ExitCode = 0
	return res
}
