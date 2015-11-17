package gosh

import (
	"io"
	"log"
	"os"
	"testing"
)

// Cmd represents a command.
// All configuration of env vars and args for this command should be done via
// the Shell.
// Not thread-safe.
type Cmd interface {
	// Stdout returns a buffer-backed Reader for this command's stdout. Must be
	// called before Start. May be called more than once; each invocation creates
	// a new buffer.
	Stdout() io.Reader

	// Stderr returns a buffer-backed Reader for this command's stderr. Must be
	// called before Start. May be called more than once; each invocation creates
	// a new buffer.
	Stderr() io.Reader

	// Start starts this command. May produce an error.
	Start()

	// AwaitReady waits for the child process to call SendReady. Must not be
	// called before Start or after Wait. May produce an error.
	AwaitReady()

	// AwaitVars waits for the child process to send values for the given vars
	// (using SendVars). Must not be called before Start or after Wait. May
	// produce an error.
	AwaitVars(keys ...string) map[string]string

	// Wait waits for this command to exit. May produce an error.
	Wait()

	// Run calls Start followed by Wait. May produce an error.
	Run()

	// Output calls Start followed by Wait, then returns this command's stdout.
	// May produce an error.
	Output() []byte

	// CombinedOutput calls Start followed by Wait, then returns this command's
	// combined stdout and stderr. May produce an error.
	CombinedOutput() []byte

	// Process returns the underlying process handle for this command.
	Process() *os.Process
}

// Shell represents a shell with an environment (a set of vars).
// Not thread-safe.
type Shell interface {
	// Err returns the most recent error, if any.
	Err() error

	// Opts returns the ShellOpts struct for this Shell, with default values
	// filled in.
	Opts() ShellOpts

	// Cmd returns a Cmd.
	Cmd(env []string, name string, args ...string) Cmd

	// Fn returns a Cmd for an invocation of the given registered Fn.
	Fn(env []string, fn *Fn, args ...interface{}) Cmd

	// Set sets the given env vars, of the form "key=value" or "key=".
	Set(vars ...string)

	// Get returns the value of the given env var.
	Get(name string) string

	// Env returns this Shell's env vars, excluding preexisting vars.
	Env() []string

	// AppendArgs configures this Shell to append the given args to all subsequent
	// commands that it runs. For example, can be used to propagate logging flags
	// to all child processes.
	AppendArgs(args ...string)

	// Wait waits for all commands started by this Shell to exit. Produces an
	// error if any individual command's Wait failed.
	Wait()

	// BuildGoPkg compiles a Go package using the "go build" command and writes
	// the resulting binary to ShellOpts.BinDir. Returns the absolute path to the
	// binary. May produce an error. Included in Shell for convenience, but could
	// have just as easily been provided as a utility function.
	BuildGoPkg(pkg string, flags ...string) string

	// MakeTempFile creates a new temporary file in os.TempDir, opens the file for
	// reading and writing, and returns the resulting *os.File. May produce an
	// error.
	MakeTempFile() *os.File

	// MakeTempDir creates a new temporary directory in os.TempDir and returns the
	// path of the new directory. May produce an error.
	MakeTempDir() string

	// Pushd behaves like Bash pushd. May produce an error.
	Pushd(dir string)

	// Popd behaves like Bash popd. May produce an error.
	Popd()

	// Cleanup cleans up all resources (child processes, temporary files and
	// directories) associated with this Shell.
	Cleanup()
}

// ShellOpts configures Shell.
type ShellOpts struct {
	// If not nil, errors trigger T.Fatal instead of panic.
	T *testing.T
	// If true, errors are logged but do not trigger T.Fatal or panic. Errors can
	// be accessed via Shell.Err(). Shell and Cmd interface comments specify which
	// methods can produce errors. All Shell and Cmd methods except Shell.Err()
	// and Shell.Cleanup() panic if Shell.Err() is not nil.
	NoDieOnErr bool
	// By default, child stdout and stderr are propagated up to the parent's
	// stdout and stderr. If SuppressChildOutput is true, child stdout and stderr
	// are not propagated up.
	// If not specified, defaults to (GOSH_SUPPRESS_CHILD_OUTPUT != "").
	SuppressChildOutput bool
	// If specified, each child's stdout and stderr streams are also piped to
	// files in this directory.
	// If not specified, defaults to GOSH_CHILD_OUTPUT_DIR.
	ChildOutputDir string
	// Directory where BuildGoPkg() writes compiled binaries.
	// If not specified, defaults to GOSH_BIN_DIR.
	BinDir string
}

// NewShell returns a new Shell. May produce an error.
func NewShell(opts ShellOpts) Shell {
	sh, err := newShell(opts)
	sh.setErr(err)
	return sh
}

var calledRunFnAndExitIfChild = false

// RunFnAndExitIfChild should be called first thing in main() or TestMain(),
// before flags are parsed. In the parent process, it returns immediately with
// no effect. In a child process for a Shell.Fn() command, it runs the specified
// function, then exits.
func RunFnAndExitIfChild() {
	calledRunFnAndExitIfChild = true
	s := os.Getenv(invocationEnv)
	if s == "" {
		return
	}
	WatchParent()
	ExitOnTerminationSignal()
	if name, args, err := decInvocation(s); err != nil {
		log.Fatal(err)
	} else {
		if err := Call(name, args...); err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	}
}
