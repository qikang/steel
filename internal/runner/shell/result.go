package shell

// Result is what the shell module returns after a remote exec.
// Stdout/Stderr are already streamed to the terminal and to the
// per-host log file, OK is set when the remote command exited 0.
//
// Result lives in the shell package because Run is its primary
// producer; the orchestrator (top-level runner package) imports
// shell for both the Run entry point and the Result type. The
// orchestrator also builds a Result for copy modules (which
// return error, not Result), so Result is shared between the two
// module paths even though the type itself originates here.
type Result struct {
	Host     string
	OK       bool
	ExitCode int    // 0 on success; -1 if the SSH layer didn't surface a code
	Stdout   string // captured for tests / programmatic callers
	Stderr   string
	Err      error
}
