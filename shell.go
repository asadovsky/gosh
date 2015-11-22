package gosh

import (
	"bytes"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime/debug"
	"sync"
	"syscall"
	"testing"
	"time"
)

const invocationEnv = "GOSH_INVOCATION"

////////////////////////////////////////////////////////////////////////////////
// Cmd

// TODO: Add timeout to AwaitReady, AwaitVars, Wait, Run, etc.

// Cmd represents a command.
// All configuration of env vars and args for this command should be done via
// the Shell.
// Not thread-safe.
type Cmd struct {
	c             *exec.Cmd
	sh            *Shell
	calledStart   bool
	stdoutWriters []io.Writer
	stderrWriters []io.Writer
	condReady     *sync.Cond
	ready         bool // protected by condReady.L
	condVars      *sync.Cond
	vars          map[string]string // protected by condVars.L
}

// Stdout returns a buffer-backed Reader for this command's stdout. Must be
// called before Start. May be called more than once; each invocation creates a
// new buffer.
func (c *Cmd) Stdout() io.Reader {
	c.sh.ok()
	res, err := c.stdout()
	c.sh.SetErr(err)
	return res
}

// Stderr returns a buffer-backed Reader for this command's stderr. Must be
// called before Start. May be called more than once; each invocation creates a
// new buffer.
func (c *Cmd) Stderr() io.Reader {
	c.sh.ok()
	res, err := c.stderr()
	c.sh.SetErr(err)
	return res
}

// Start starts this command. May produce an error.
func (c *Cmd) Start() {
	c.sh.ok()
	c.sh.SetErr(c.start())
}

// AwaitReady waits for the child process to call SendReady. Must not be called
// before Start or after Wait. May produce an error.
func (c *Cmd) AwaitReady() {
	c.sh.ok()
	c.sh.SetErr(c.awaitReady())
}

// AwaitVars waits for the child process to send values for the given vars
// (using SendVars). Must not be called before Start or after Wait. May produce
// an error.
func (c *Cmd) AwaitVars(keys ...string) map[string]string {
	c.sh.ok()
	res, err := c.awaitVars(keys...)
	c.sh.SetErr(err)
	return res
}

// Wait waits for this command to exit. May produce an error.
func (c *Cmd) Wait() {
	c.sh.ok()
	c.sh.SetErr(c.wait())
}

// Run calls Start followed by Wait. May produce an error.
func (c *Cmd) Run() {
	c.sh.ok()
	c.sh.SetErr(c.run())
}

// Output calls Start followed by Wait, then returns this command's stdout. May
// produce an error.
func (c *Cmd) Output() []byte {
	c.sh.ok()
	res, err := c.output()
	c.sh.SetErr(err)
	return res
}

// CombinedOutput calls Start followed by Wait, then returns this command's
// combined stdout and stderr. May produce an error.
func (c *Cmd) CombinedOutput() []byte {
	c.sh.ok()
	res, err := c.combinedOutput()
	c.sh.SetErr(err)
	return res
}

// Process returns the underlying process handle for this command.
func (c *Cmd) Process() *os.Process {
	c.sh.ok()
	return c.process()
}

////////////////////////////////////////
// Cmd internals

func newCmd(sh *Shell, name string, args ...string) *Cmd {
	return &Cmd{
		c:             exec.Command(name, args...),
		sh:            sh,
		stdoutWriters: []io.Writer{},
		stderrWriters: []io.Writer{},
		condReady:     sync.NewCond(&sync.Mutex{}),
		condVars:      sync.NewCond(&sync.Mutex{}),
		vars:          map[string]string{},
	}
}

var alreadyCalledStart = fmt.Errorf("already called start")

func (c *Cmd) addWriter(writers *[]io.Writer, w io.Writer) {
	*writers = append(*writers, w)
	if f, ok := w.(*os.File); ok {
		c.c.ExtraFiles = append(c.c.ExtraFiles, f)
	}
}

// recvWriter listens for gosh messages from a child process.
type recvWriter struct {
	c          *Cmd
	buf        bytes.Buffer
	readPrefix bool // if true, we've read len(msgPrefix) for the current line
	skipLine   bool // if true, ignore bytes until next '\n'
}

func (w *recvWriter) Write(p []byte) (n int, err error) {
	for _, b := range p {
		if b == '\n' {
			if w.readPrefix && !w.skipLine {
				m := msg{}
				if err := json.Unmarshal(w.buf.Bytes(), &m); err != nil {
					return 0, err
				}
				switch m.Type {
				case typeReady:
					w.c.condReady.L.Lock()
					w.c.ready = true
					w.c.condReady.Signal()
					w.c.condReady.L.Unlock()
				case typeVars:
					w.c.condVars.L.Lock()
					w.c.vars = mergeMaps(w.c.vars, m.Vars)
					w.c.condVars.Signal()
					w.c.condVars.L.Unlock()
				default:
					return 0, fmt.Errorf("unknown message type: %q", m.Type)
				}
			}
			// Reset state for next line.
			w.readPrefix, w.skipLine = false, false
			w.buf.Reset()
		} else if !w.skipLine {
			w.buf.WriteByte(b)
			if !w.readPrefix && w.buf.Len() == len(msgPrefix) {
				w.readPrefix = true
				prefix := string(w.buf.Next(len(msgPrefix)))
				if prefix != msgPrefix {
					w.skipLine = true
				}
			}
		}
	}
	return len(p), nil
}

func (c *Cmd) initMultiWriter(f *os.File, t string) (io.Writer, error) {
	var writers *[]io.Writer
	if f == os.Stdout {
		writers = &c.stdoutWriters
	} else {
		writers = &c.stderrWriters
	}
	if !c.sh.opts.SuppressChildOutput {
		c.addWriter(writers, f)
	}
	if c.sh.opts.ChildOutputDir != "" {
		suffix := "stderr"
		if f == os.Stdout {
			suffix = "stdout"
		}
		name := filepath.Join(c.sh.opts.ChildOutputDir, filepath.Base(c.c.Path)+"."+t+"."+suffix)
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		c.addWriter(writers, f)
	}
	if f == os.Stdout {
		c.addWriter(writers, &recvWriter{c: c})
	}
	return io.MultiWriter(*writers...), nil
}

func (c *Cmd) stdout() (io.Reader, error) {
	if c.calledStart {
		return nil, alreadyCalledStart
	}
	var buf bytes.Buffer
	c.addWriter(&c.stdoutWriters, &buf)
	return &buf, nil
}

func (c *Cmd) stderr() (io.Reader, error) {
	if c.calledStart {
		return nil, alreadyCalledStart
	}
	var buf bytes.Buffer
	c.addWriter(&c.stderrWriters, &buf)
	return &buf, nil
}

// TODO: Make errors bubble up to Await*() in addition to Wait().
func (c *Cmd) start() error {
	if c.calledStart {
		return alreadyCalledStart
	}
	c.calledStart = true
	if c.c.Stdout != nil || c.c.Stderr != nil { // invariant check
		log.Fatal(c.c.Stdout, c.c.Stderr)
	}
	// Set up stdout and stderr.
	t := time.Now().UTC().Format("20060102.150405.999")
	var err error
	if c.c.Stdout, err = c.initMultiWriter(os.Stdout, t); err != nil {
		return err
	}
	if c.c.Stderr, err = c.initMultiWriter(os.Stderr, t); err != nil {
		return err
	}
	// TODO: Wrap every child process with a "supervisor" process that calls
	// WatchParent().
	return c.c.Start()
}

func (c *Cmd) awaitReady() error {
	// http://golang.org/pkg/sync/#Cond.Wait
	c.condReady.L.Lock()
	for !c.ready {
		c.condReady.Wait()
	}
	c.condReady.L.Unlock()
	return nil
}

func (c *Cmd) awaitVars(keys ...string) (map[string]string, error) {
	wantKeys := map[string]bool{}
	for _, key := range keys {
		wantKeys[key] = true
	}
	res := map[string]string{}
	updateRes := func() {
		for k, v := range c.vars {
			if _, ok := wantKeys[k]; ok {
				res[k] = v
			}
		}
	}
	// http://golang.org/pkg/sync/#Cond.Wait
	c.condVars.L.Lock()
	updateRes()
	for len(res) < len(wantKeys) {
		c.condVars.Wait()
		updateRes()
	}
	c.condVars.L.Unlock()
	return res, nil
}

func (c *Cmd) wait() error {
	return c.c.Wait()
}

func (c *Cmd) run() error {
	if err := c.start(); err != nil {
		return err
	}
	return c.wait()
}

func (c *Cmd) output() ([]byte, error) {
	var buf bytes.Buffer
	c.addWriter(&c.stdoutWriters, &buf)
	err := c.run()
	return buf.Bytes(), err
}

func (c *Cmd) combinedOutput() ([]byte, error) {
	var buf bytes.Buffer
	c.addWriter(&c.stdoutWriters, &buf)
	c.addWriter(&c.stderrWriters, &buf)
	err := c.run()
	return buf.Bytes(), err
}

func (c *Cmd) process() *os.Process {
	return c.c.Process
}

////////////////////////////////////////////////////////////////////////////////
// Shell

// Shell represents a shell with an environment (a set of vars).
// Not thread-safe.
type Shell struct {
	err           error
	opts          ShellOpts
	vars          map[string]string
	args          []string
	cmds          []*Cmd
	tempFiles     []*os.File
	tempDirs      []string
	dirStack      []string   // for pushd/popd
	cleanupLock   sync.Mutex // protects calledCleanup
	calledCleanup bool
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
func NewShell(opts ShellOpts) *Shell {
	sh, err := newShell(opts)
	sh.SetErr(err)
	return sh
}

// SetErr sets the given error. Consequent behavior depends on ShellOpts.
func (sh *Shell) SetErr(err error) {
	if err == nil || sh.err != nil {
		return
	}
	sh.err = err
	if sh.opts.NoDieOnErr {
		sh.log(true, err.Error())
	} else {
		if sh.opts.T == nil {
			panic(err)
		} else {
			debug.PrintStack()
			sh.opts.T.Fatal(err)
		}
	}
}

// Err returns the most recent error, if any.
func (sh *Shell) Err() error {
	return sh.err
}

// Opts returns the ShellOpts for this Shell, with default values filled in.
func (sh *Shell) Opts() ShellOpts {
	return sh.opts
}

// Cmd returns a Cmd for an invocation of the named program.
func (sh *Shell) Cmd(env []string, name string, args ...string) *Cmd {
	sh.ok()
	return sh.cmd(env, name, args...)
}

// Fn returns a Cmd for an invocation of the given registered Fn.
func (sh *Shell) Fn(env []string, fn *Fn, args ...interface{}) *Cmd {
	sh.ok()
	res, err := sh.fn(env, fn, args...)
	sh.SetErr(err)
	return res
}

// Set sets the given env vars, of the form "key=value" or "key=".
func (sh *Shell) Set(vars ...string) {
	sh.ok()
	sh.set(vars...)
}

// Get returns the value of the given env var.
func (sh *Shell) Get(name string) string {
	sh.ok()
	return sh.get(name)
}

// Env returns this Shell's env vars, excluding preexisting vars.
func (sh *Shell) Env() []string {
	sh.ok()
	return sh.env()
}

// AppendArgs configures this Shell to append the given args to all subsequent
// commands that it runs. For example, can be used to propagate logging flags to
// all child processes.
func (sh *Shell) AppendArgs(args ...string) {
	sh.ok()
	sh.appendArgs(args...)
}

// Wait waits for all commands started by this Shell to exit. Produces an error
// if any individual command's Wait failed.
func (sh *Shell) Wait() {
	sh.ok()
	sh.SetErr(sh.wait())
}

// BuildGoPkg compiles a Go package using the "go build" command and writes the
// resulting binary to ShellOpts.BinDir. Returns the absolute path to the
// binary. May produce an error. Included in Shell for convenience, but could
// have just as easily been provided as a utility function.
func (sh *Shell) BuildGoPkg(pkg string, flags ...string) string {
	sh.ok()
	res, err := sh.buildGoPkg(pkg, flags...)
	sh.SetErr(err)
	return res
}

// MakeTempFile creates a new temporary file in os.TempDir, opens the file for
// reading and writing, and returns the resulting *os.File. May produce an
// error.
func (sh *Shell) MakeTempFile() *os.File {
	sh.ok()
	res, err := sh.makeTempFile()
	sh.SetErr(err)
	return res
}

// MakeTempDir creates a new temporary directory in os.TempDir and returns the
// path of the new directory. May produce an error.
func (sh *Shell) MakeTempDir() string {
	sh.ok()
	res, err := sh.makeTempDir()
	sh.SetErr(err)
	return res
}

// Pushd behaves like Bash pushd. May produce an error.
func (sh *Shell) Pushd(dir string) {
	sh.ok()
	sh.SetErr(sh.pushd(dir))
}

// Popd behaves like Bash popd. May produce an error.
func (sh *Shell) Popd() {
	sh.ok()
	sh.SetErr(sh.popd())
}

// Cleanup cleans up all resources (child processes, temporary files and
// directories) associated with this Shell.
func (sh *Shell) Cleanup() {
	sh.cleanupLock.Lock()
	if sh.calledCleanup {
		panic("already called cleanup")
	}
	sh.calledCleanup = true
	sh.cleanupLock.Unlock()
	sh.cleanup()
}

////////////////////////////////////////
// Shell internals

func newShell(opts ShellOpts) (*Shell, error) {
	// Set this process's PGID to its PID so that its child processes can be
	// identified reliably.
	// http://man7.org/linux/man-pages/man2/setpgid.2.html
	if err := syscall.Setpgid(0, 0); err != nil {
		return nil, err
	}
	if !opts.SuppressChildOutput {
		opts.SuppressChildOutput = os.Getenv("GOSH_SUPPRESS_CHILD_OUTPUT") != ""
	}
	if opts.ChildOutputDir == "" {
		opts.ChildOutputDir = os.Getenv("GOSH_CHILD_OUTPUT_DIR")
	}
	sh := &Shell{
		opts:      opts,
		vars:      map[string]string{},
		args:      []string{},
		cmds:      []*Cmd{},
		tempFiles: []*os.File{},
		tempDirs:  []string{},
		dirStack:  []string{},
	}
	if sh.opts.BinDir == "" {
		sh.opts.BinDir = os.Getenv("GOSH_BIN_DIR")
		if sh.opts.BinDir == "" {
			var err error
			if sh.opts.BinDir, err = sh.makeTempDir(); err != nil {
				return nil, err
			}
		}
	}
	// Run sh.cleanup() (if needed) when a termination signal is received.
	OnTerminationSignal(func(sig os.Signal) {
		sh.log(false, fmt.Sprintf("Received signal: %v", sig))
		sh.cleanupLock.Lock()
		if !sh.calledCleanup {
			sh.calledCleanup = true
			sh.cleanupLock.Unlock()
			sh.cleanup()
		} else {
			sh.cleanupLock.Unlock()
		}
		// http://www.gnu.org/software/bash/manual/html_node/Exit-Status.html
		// Unfortunately, os.Signal does not surface the signal number.
		os.Exit(1)
	})
	return sh, nil
}

func (sh *Shell) log(severe bool, msg string) {
	prefix := "WARNING"
	if severe {
		prefix = "ERROR"
	}
	msg = fmt.Sprintf("%s: %s\n", prefix, msg)
	if sh.opts.T == nil {
		log.Print(msg)
	} else {
		sh.opts.T.Log(msg)
	}
}

func (sh *Shell) ok() {
	if sh.err != nil {
		panic(sh.err)
	}
	sh.cleanupLock.Lock()
	if sh.calledCleanup {
		panic("already called cleanup")
	}
	sh.cleanupLock.Unlock()
}

func (sh *Shell) cmd(env []string, name string, args ...string) *Cmd {
	c := newCmd(sh, name, append(args, sh.args...)...)
	c.c.Env = mapToSlice(mergeMaps(sliceToMap(os.Environ()), sh.vars, sliceToMap(env)))
	sh.cmds = append(sh.cmds, c)
	return c
}

func (sh *Shell) fn(env []string, fn *Fn, args ...interface{}) (*Cmd, error) {
	// Safeguard against the developer forgetting to call RunFnAndExitIfChild,
	// which would otherwise lead to recursive invocation of this program.
	if !calledRunFnAndExitIfChild {
		return nil, fmt.Errorf("did not call RunFnAndExitIfChild")
	}
	b, err := encInvocation(fn.name, args...)
	if err != nil {
		return nil, err
	}
	env = mapToSlice(mergeMaps(sliceToMap(env), map[string]string{invocationEnv: string(b)}))
	return sh.cmd(env, os.Args[0]), nil
}

func (sh *Shell) set(vars ...string) {
	for _, kv := range vars {
		k, v := splitKeyValue(kv)
		if v == "" {
			delete(sh.vars, k)
		} else {
			sh.vars[k] = v
		}
	}
}

func (sh *Shell) get(name string) string {
	return sh.vars[name]
}

func (sh *Shell) env() []string {
	return mapToSlice(sh.vars)
}

func (sh *Shell) appendArgs(args ...string) {
	sh.args = append(sh.args, args...)
}

func (sh *Shell) wait() error {
	var res error
	for _, c := range sh.cmds {
		if err := c.wait(); err != nil {
			sh.log(true, fmt.Sprintf("Cmd.Wait() failed: %v", err))
			if res == nil {
				res = err
			}
		}
	}
	return res
}

func (sh *Shell) buildGoPkg(pkg string, flags ...string) (string, error) {
	binPath := filepath.Join(sh.opts.BinDir, path.Base(pkg))
	// If this binary has already been built, don't rebuild it.
	if _, err := os.Stat(binPath); err == nil {
		return binPath, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	// Build binary to tempBinPath, then move it to binPath.
	tempDir, err := ioutil.TempDir(sh.opts.BinDir, "")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tempDir)
	tempBinPath := filepath.Join(tempDir, path.Base(pkg))
	args := []string{"build", "-x", "-o", tempBinPath}
	args = append(args, flags...)
	args = append(args, pkg)
	if err := sh.cmd(nil, "go", args...).run(); err != nil {
		return "", err
	}
	if err := os.Rename(tempBinPath, binPath); err != nil {
		return "", err
	}
	return binPath, nil
}

func (sh *Shell) makeTempFile() (*os.File, error) {
	f, err := ioutil.TempFile("", "")
	if err != nil {
		return nil, err
	}
	sh.tempFiles = append(sh.tempFiles, f)
	return f, nil
}

func (sh *Shell) makeTempDir() (string, error) {
	name, err := ioutil.TempDir("", "")
	if err != nil {
		return "", err
	}
	sh.tempDirs = append(sh.tempDirs, name)
	return name, nil
}

func (sh *Shell) pushd(dir string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	sh.dirStack = append(sh.dirStack, cwd)
	return nil
}

func (sh *Shell) popd() error {
	if len(sh.dirStack) == 0 {
		return fmt.Errorf("dir stack is empty")
	}
	dir := sh.dirStack[len(sh.dirStack)-1]
	if err := os.Chdir(dir); err != nil {
		return err
	}
	sh.dirStack = sh.dirStack[:len(sh.dirStack)-1]
	return nil
}

// forEachRunningChild applies fn to each running child process.
func (sh *Shell) forEachRunningChild(fn func(*os.Process)) bool {
	anyRunning := false
	for _, c := range sh.cmds {
		if c.c.Process == nil {
			continue // not started
		}
		if pgid, err := syscall.Getpgid(c.c.Process.Pid); err != nil || pgid != os.Getpid() {
			continue // not our child
		}
		anyRunning = true
		fn(c.c.Process)
	}
	return anyRunning
}

func (sh *Shell) cleanup() {
	// Note, newShell() calls syscall.Setpgid().
	if os.Getpid() != syscall.Getpgrp() {
		panic(fmt.Sprint(os.Getpid(), syscall.Getpgrp()))
	}
	// Terminate all children that are still running. Try SIGTERM first; if that
	// doesn't work, use SIGKILL.
	// https://golang.org/pkg/os/#Process.Signal
	anyRunning := sh.forEachRunningChild(func(p *os.Process) {
		if err := p.Signal(syscall.SIGTERM); err != nil {
			sh.log(false, fmt.Sprintf("%d.Signal(SIGTERM) failed: %v", p.Pid, err))
		}
	})
	// If any child is still running, wait for 20ms.
	if anyRunning {
		time.Sleep(20 * time.Millisecond)
		anyRunning = sh.forEachRunningChild(func(p *os.Process) {
			sh.log(false, fmt.Sprintf("process %d did not die", p.Pid))
		})
	}
	// If any child is still running, wait for another second, then send SIGKILL
	// to all running children.
	if anyRunning {
		time.Sleep(time.Second)
		sh.log(false, "sending SIGKILL to all remaining child processes")
		sh.forEachRunningChild(func(p *os.Process) {
			if err := p.Kill(); err != nil {
				sh.log(false, fmt.Sprintf("%d.Kill() failed: %v", p.Pid, err))
			}
		})
		sh.forEachRunningChild(func(p *os.Process) {
			sh.log(false, fmt.Sprintf("process %d did not die", p.Pid))
		})
	}
	// Close and delete all temporary files.
	for _, tempFile := range sh.tempFiles {
		name := tempFile.Name()
		if err := tempFile.Close(); err != nil {
			sh.log(false, fmt.Sprintf("%q.Close() failed: %v", name, err))
		}
		if err := os.RemoveAll(name); err != nil {
			sh.log(false, fmt.Sprintf("os.RemoveAll(%q) failed: %v", name, err))
		}
	}
	// Delete all temporary directories.
	for _, tempDir := range sh.tempDirs {
		if err := os.RemoveAll(tempDir); err != nil {
			sh.log(false, fmt.Sprintf("os.RemoveAll(%q) failed: %v", tempDir, err))
		}
	}
}

////////////////////////////////////////////////////////////////////////////////
// Public utilities

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

// Run calls RunFnAndExitIfChild(), then returns m.Run(). Exported so that
// TestMain functions can simply call os.Exit(gosh.Run(m)).
func Run(m *testing.M) int {
	RunFnAndExitIfChild()
	return m.Run()
}

////////////////////////////////////////////////////////////////////////////////
// invocation

type invocation struct {
	Name string
	Args []interface{}
}

// encInvocation encodes an invocation.
func encInvocation(name string, args ...interface{}) (string, error) {
	inv := invocation{Name: name, Args: args}
	buf := new(bytes.Buffer)
	if err := gob.NewEncoder(buf).Encode(inv); err != nil {
		return "", fmt.Errorf("failed to encode invocation: %v", err)
	}
	// Hex-encode the gob-encoded bytes so that the result can be used as an env
	// var value.
	return hex.EncodeToString(buf.Bytes()), nil
}

// decInvocation decodes an invocation.
func decInvocation(s string) (name string, args []interface{}, err error) {
	var inv invocation
	b, err := hex.DecodeString(s)
	if err == nil {
		err = gob.NewDecoder(bytes.NewReader(b)).Decode(&inv)
	}
	if err != nil {
		return "", nil, fmt.Errorf("failed to decode invocation: %v", err)
	}
	return inv.Name, inv.Args, nil
}
