package gosh

// TODO:
// - Single-binary mechanism, by means of a function registry
// - Introspection mechanism, e.g. to see which commands are running

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime/debug"
	"testing"
)

// Cmd represents a command. Errors typically originate from exec.Cmd.
// Not thread-safe.
// TODO: Provide access to streams (stdin, stdout, stderr).
type Cmd interface {
	// Start starts this command.
	Start() error

	// AwaitReady waits for the child to call SendReady. Must be called after
	// Start and before Wait.
	AwaitReady() error

	// AwaitVars waits for the child to send values for the given vars (using
	// SendVars). Must be called after Start and before Wait.
	AwaitVars(vars ...string) (error, map[string]string)

	// Wait waits for this command to complete.
	Wait() error

	// Run starts this command and waits for it to complete.
	Run() error

	// Process returns the underlying process handle for this command.
	Process() *os.Process
}

// Shell represents a shell with an environment (a set of vars).
// Not thread-safe.
type Shell interface {
	// Cmd returns a Cmd.
	Cmd(name string, args ...string) Cmd

	// Set sets the given env vars, of the form "KEY=value" or "KEY=".
	Set(vars ...string)

	// Get returns the value of the given env var.
	Get(name string) string

	// Env returns this Shell's env vars, excluding preexisting vars.
	Env() []string

	// AppendArgs configures this Shell to append the given args to all subsequent
	// commands that it runs.
	AppendArgs(args ...string)

	// Wait waits for all commands started by this Shell to complete. Returns nil
	// if all commands ran successfully. Otherwise, returns some command's error.
	Wait() error

	// BinDir returns the directory where BuildGoPkg() writes compiled binaries.
	// Defaults to SHELL_BIN_DIR, if set.
	BinDir() string

	// BuildGoPkg compiles a Go package using the "go build" command and writes
	// the resulting binary to BinDir(). Returns the absolute path to the binary.
	// Included in Shell for convenience, but could have just as easily been
	// provided as a utility function.
	BuildGoPkg(pkg string, flags ...string) (string, error)

	// MakeTempDir creates a new temporary directory in os.TempDir and returns the
	// path of the new directory.
	MakeTempDir() (string, error)

	// MakeTempFile creates a new temporary file in os.TempDir, opens the file for
	// reading and writing, and returns the resulting *os.File.
	MakeTempFile() (*os.File, error)

	// Pushd behaves like Bash pushd.
	Pushd(dir string) error

	// Popd behaves like Bash popd.
	Popd() error
}

// ShellOpts configures Shell.
type ShellOpts struct {
	// If not nil, all errors trigger T.Fatal.
	T *testing.T
	// If true, all errors trigger panic.
	PanicOnError bool
}

// New returns a new Shell.
func New(opts ShellOpts) (Shell, func(), error) {
	sh := &shell{
		opts:      opts,
		vars:      map[string]string{},
		args:      []string{},
		cmds:      []*cmd{},
		tempDirs:  []string{},
		tempFiles: []*os.File{},
		dirStack:  []string{},
	}
	if sh.binDir = os.Getenv("SHELL_BIN_DIR"); sh.binDir == "" {
		var err error
		if sh.binDir, err = sh.MakeTempDir(); err != nil {
			return nil, nil, sh.err(err)
		}
	}
	return sh, sh.cleanup, nil
}

type cmd struct {
	exec.Cmd
}

func (c *cmd) AwaitReady() error {
	// FIXME
	return nil
}

func (c *cmd) AwaitVars(vars ...string) (error, map[string]string) {
	// FIXME
	return nil, nil
}

func (c *cmd) Process() *os.Process {
	return c.Cmd.Process
}

type shell struct {
	opts      ShellOpts
	vars      map[string]string
	args      []string
	cmds      []*cmd
	tempDirs  []string
	tempFiles []*os.File
	binDir    string
	dirStack  []string
}

func (sh *shell) err(err error) error {
	if err != nil {
		if sh.opts.T != nil {
			debug.PrintStack()
			sh.opts.T.Fatal(err)
		}
		if sh.opts.PanicOnError {
			panic(err)
		}
	}
	return err
}

func (sh *shell) Cmd(name string, args ...string) Cmd {
	c := &cmd{Cmd: *exec.Command(name, append(args, sh.args...)...)}
	sh.cmds = append(sh.cmds, c)
	return c
}

func (sh *shell) Set(vars ...string) {
	for _, kv := range vars {
		k, v := splitKeyValue(kv)
		if v == "" {
			delete(sh.vars, k)
		} else {
			sh.vars[k] = v
		}
	}
}

func (sh *shell) Get(name string) string {
	return sh.vars[name]
}

func (sh *shell) Env() []string {
	return mapToSlice(sh.vars)
}

func (sh *shell) AppendArgs(args ...string) {
	sh.args = append(sh.args, args...)
}

func (sh *shell) Wait() error {
	var res error
	for _, cmd := range sh.cmds {
		if err := cmd.Wait(); err != nil {
			log.Printf("WARNING: cmd.Wait() failed: %v, %v", cmd.Cmd, err)
			res = err
		}
	}
	return res
}

func (sh *shell) BinDir() string {
	return sh.binDir
}

func (sh *shell) BuildGoPkg(pkg string, flags ...string) (string, error) {
	binPath := filepath.Join(sh.BinDir(), path.Base(pkg))
	// If this binary has already been built, don't rebuild it.
	if _, err := os.Stat(binPath); err == nil {
		return binPath, nil
	} else if !os.IsNotExist(err) {
		return "", sh.err(err)
	}
	// Build binary to tempBinPath, then move it to binPath.
	tempDir, err := ioutil.TempDir(sh.BinDir(), "")
	if err != nil {
		return "", sh.err(err)
	}
	defer os.RemoveAll(tempDir)
	tempBinPath := filepath.Join(tempDir, path.Base(pkg))
	args := []string{"build", "-x", "-o", tempBinPath}
	args = append(args, flags...)
	args = append(args, pkg)
	err = sh.Cmd("go", args...).Run()
	if err != nil {
		return "", sh.err(err)
	}
	if err := os.Rename(tempBinPath, binPath); err != nil {
		return "", sh.err(err)
	}
	return binPath, nil
}

func (sh *shell) MakeTempDir() (string, error) {
	name, err := ioutil.TempDir("", "")
	if err != nil {
		return "", sh.err(err)
	}
	sh.tempDirs = append(sh.tempDirs, name)
	return name, nil
}

func (sh *shell) MakeTempFile() (*os.File, error) {
	f, err := ioutil.TempFile("", "")
	if err != nil {
		return nil, sh.err(err)
	}
	sh.tempFiles = append(sh.tempFiles, f)
	return f, nil
}

func (sh *shell) Pushd(dir string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return sh.err(err)
	}
	if err := os.Chdir(dir); err != nil {
		return sh.err(err)
	}
	sh.dirStack = append(sh.dirStack, cwd)
	return nil
}

func (sh *shell) Popd() error {
	if len(sh.dirStack) == 0 {
		return sh.err(fmt.Errorf("dir stack is empty"))
	}
	dir := sh.dirStack[len(sh.dirStack)-1]
	if err := os.Chdir(dir); err != nil {
		return sh.err(err)
	}
	sh.dirStack = sh.dirStack[:len(sh.dirStack)-1]
	return nil
}

func (sh *shell) cleanup() {
	// TODO: Stop or kill all running processes.
	for _, tempDir := range sh.tempDirs {
		if err := os.RemoveAll(tempDir); err != nil {
			log.Printf("WARNING: os.RemoveAll(%q) failed: %v", tempDir, err)
		}
	}
	for _, tempFile := range sh.tempFiles {
		if err := tempFile.Close(); err != nil {
			log.Printf("WARNING: %q.Close() failed: %v", tempFile.Name(), err)
		}
		if err := os.RemoveAll(tempFile.Name()); err != nil {
			log.Printf("WARNING: os.RemoveAll(%q) failed: %v", tempFile.Name(), err)
		}
	}
}
