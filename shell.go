package gosh

// TODO: Add single-binary mechanism, by means of a function registry.

import (
	"os"
	"testing"
)

// Cmd represents a command.
// Not thread-safe.
// TODO: Add hooks to modify env vars or args for unstarted commands?
type Cmd interface {
	// Start starts this command. May produce an error.
	Start()

	// AwaitReady waits for the child process to call SendReady. Must not be
	// called before Start or after Wait. May produce an error.
	AwaitReady()

	// AwaitVars waits for the child process to send values for the given vars
	// (using SendVars). Must not be called before Start or after Wait. May
	// produce an error.
	AwaitVars(keys ...string) map[string]string

	// Wait waits for this command to complete. May produce an error.
	Wait()

	// Run starts this command and waits for it to complete. May produce an error.
	Run()

	// Output runs this command and returns its standard output. May produce an
	// error.
	Output() []byte

	// CombinedOutput runs this command and returns its combined standard output
	// and standard error. May produce an error.
	CombinedOutput() []byte

	// Process returns the underlying process handle for this command.
	Process() *os.Process
}

// Shell represents a shell with an environment (a set of vars).
// Not thread-safe.
// TODO: Propagate certain flags (e.g. logging flags) to subprocesses?
type Shell interface {
	// Err returns the most recent error, if any.
	Err() error

	// Opts returns the ShellOpts struct for this Shell, with default values
	// filled in.
	Opts() ShellOpts

	// Cmd returns a Cmd.
	// TODO: Add env parameter?
	Cmd(name string, args ...string) Cmd

	// Func returns a Cmd for the function registered with the given name.
	// TODO: Add env parameter?
	Func(name string, args ...interface{}) Cmd

	// Set sets the given env vars, of the form "key=value" or "key=".
	Set(vars ...string)

	// Get returns the value of the given env var.
	Get(name string) string

	// Env returns this Shell's env vars, excluding preexisting vars.
	Env() []string

	// AppendArgs configures this Shell to append the given args to all subsequent
	// commands that it runs.
	AppendArgs(args ...string)

	// Wait waits for all commands started by this Shell to complete. Produces an
	// error if any individual command's Wait() failed.
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
	// be accessed via Err(). Shell and Cmd interface comments specify which
	// methods can produce errors. All Shell and Cmd methods except Shell.Err()
	// and Shell.Cleanup() panic if Err() is not nil.
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
