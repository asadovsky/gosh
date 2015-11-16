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
	"time"
)

const invocationEnv = "GOSH_INVOCATION"

////////////////////////////////////////////////////////////////////////////////
// Cmd

type cmd struct {
	c           *exec.Cmd
	sh          *shell
	calledStart bool
	condReady   *sync.Cond
	ready       bool // protected by condReady.L
	condVars    *sync.Cond
	vars        map[string]string // protected by condVars.L
}

func newCmd(sh *shell, name string, args ...string) *cmd {
	return &cmd{
		c:         exec.Command(name, args...),
		sh:        sh,
		condReady: sync.NewCond(&sync.Mutex{}),
		condVars:  sync.NewCond(&sync.Mutex{}),
		vars:      map[string]string{},
	}
}

func (c *cmd) Start() {
	c.sh.ok()
	c.sh.setErr(c.start())
}

func (c *cmd) AwaitReady() {
	c.sh.ok()
	c.sh.setErr(c.awaitReady())
}

func (c *cmd) AwaitVars(keys ...string) map[string]string {
	c.sh.ok()
	res, err := c.awaitVars(keys...)
	c.sh.setErr(err)
	return res
}

func (c *cmd) Wait() {
	c.sh.ok()
	c.sh.setErr(c.wait())
}

func (c *cmd) Run() {
	c.sh.ok()
	c.sh.setErr(c.run())
}

func (c *cmd) Output() []byte {
	c.sh.ok()
	res, err := c.output()
	c.sh.setErr(err)
	return res
}

func (c *cmd) CombinedOutput() []byte {
	c.sh.ok()
	res, err := c.combinedOutput()
	c.sh.setErr(err)
	return res
}

func (c *cmd) Process() *os.Process {
	c.sh.ok()
	return c.process()
}

////////////////////////////////////////
// Cmd internals

// recvWriter listens for gosh messages from a child process.
type recvWriter struct {
	c          *cmd
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

// TODO: Make errors bubble up to Await*() in addition to Wait().
func (c *cmd) newMultiWriter(t string, f *os.File) (io.Writer, error) {
	writers := []io.Writer{}
	push := func(f *os.File) {
		writers = append(writers, f)
		c.c.ExtraFiles = append(c.c.ExtraFiles, f)
	}
	if !c.sh.opts.SuppressChildOutput {
		push(f)
	}
	dir := c.sh.opts.ChildOutputDir
	if dir != "" {
		suffix := "stderr"
		if f == os.Stdout {
			suffix = "stdout"
		}
		name := filepath.Join(dir, filepath.Base(c.c.Path)+"."+t+"."+suffix)
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		push(f)
	}
	if f == os.Stdout {
		writers = append(writers, &recvWriter{c: c})
	}
	return io.MultiWriter(writers...), nil
}

func (c *cmd) start() error {
	if c.calledStart {
		return fmt.Errorf("already called start")
	}
	c.calledStart = true
	if c.c.Stdout == nil && c.c.Stderr == nil {
		// Set up stdout and stderr.
		t := time.Now().UTC().Format("20060102.150405.999")
		var err error
		if c.c.Stdout, err = c.newMultiWriter(t, os.Stdout); err != nil {
			return err
		}
		if c.c.Stderr, err = c.newMultiWriter(t, os.Stderr); err != nil {
			return err
		}
	}
	// TODO: Wrap every child process with a "supervisor" process that calls
	// WatchParent().
	return c.c.Start()
}

func (c *cmd) awaitReady() error {
	// http://golang.org/pkg/sync/#Cond.Wait
	c.condReady.L.Lock()
	for !c.ready {
		c.condReady.Wait()
	}
	c.condReady.L.Unlock()
	return nil
}

func (c *cmd) awaitVars(keys ...string) (map[string]string, error) {
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

func (c *cmd) wait() error {
	return c.c.Wait()
}

func (c *cmd) run() error {
	if err := c.start(); err != nil {
		return err
	}
	return c.wait()
}

func (c *cmd) output() ([]byte, error) {
	var buf bytes.Buffer
	c.c.Stdout = &buf
	err := c.run()
	return buf.Bytes(), err
}

func (c *cmd) combinedOutput() ([]byte, error) {
	var buf bytes.Buffer
	c.c.Stdout = &buf
	c.c.Stderr = &buf
	err := c.run()
	return buf.Bytes(), err
}

func (c *cmd) process() *os.Process {
	return c.c.Process
}

////////////////////////////////////////////////////////////////////////////////
// Shell

type shell struct {
	err           error
	opts          ShellOpts
	vars          map[string]string
	args          []string
	cmds          []*cmd
	tempFiles     []*os.File
	tempDirs      []string
	dirStack      []string   // for pushd/popd
	cleanupLock   sync.Mutex // protects calledCleanup
	calledCleanup bool
}

func newShell(opts ShellOpts) (*shell, error) {
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
	sh := &shell{
		opts:      opts,
		vars:      map[string]string{},
		args:      []string{},
		cmds:      []*cmd{},
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

func (sh *shell) Err() error {
	return sh.err
}

func (sh *shell) Opts() ShellOpts {
	sh.ok()
	return sh.getOpts()
}

func (sh *shell) Cmd(env []string, name string, args ...string) Cmd {
	sh.ok()
	return sh.cmd(env, name, args...)
}

func (sh *shell) Fn(env []string, fn *Fn, args ...interface{}) Cmd {
	sh.ok()
	res, err := sh.fn(env, fn, args...)
	sh.setErr(err)
	return res
}

func (sh *shell) Set(vars ...string) {
	sh.ok()
	sh.set(vars...)
}

func (sh *shell) Get(name string) string {
	sh.ok()
	return sh.get(name)
}

func (sh *shell) Env() []string {
	sh.ok()
	return sh.env()
}

func (sh *shell) AppendArgs(args ...string) {
	sh.ok()
	sh.appendArgs(args...)
}

func (sh *shell) Wait() {
	sh.ok()
	sh.setErr(sh.wait())
}

func (sh *shell) BuildGoPkg(pkg string, flags ...string) string {
	sh.ok()
	res, err := sh.buildGoPkg(pkg, flags...)
	sh.setErr(err)
	return res
}

func (sh *shell) MakeTempFile() *os.File {
	sh.ok()
	res, err := sh.makeTempFile()
	sh.setErr(err)
	return res
}

func (sh *shell) MakeTempDir() string {
	sh.ok()
	res, err := sh.makeTempDir()
	sh.setErr(err)
	return res
}

func (sh *shell) Pushd(dir string) {
	sh.ok()
	sh.setErr(sh.pushd(dir))
}

func (sh *shell) Popd() {
	sh.ok()
	sh.setErr(sh.popd())
}

func (sh *shell) Cleanup() {
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

func (sh *shell) log(severe bool, msg string) {
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

func (sh *shell) ok() {
	if sh.err != nil {
		panic(sh.err)
	}
	sh.cleanupLock.Lock()
	if sh.calledCleanup {
		panic("already called cleanup")
	}
	sh.cleanupLock.Unlock()
}

func (sh *shell) setErr(err error) {
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

func (sh *shell) getOpts() ShellOpts {
	return sh.opts
}

func (sh *shell) cmd(env []string, name string, args ...string) *cmd {
	c := newCmd(sh, name, append(args, sh.args...)...)
	c.c.Env = mapToSlice(mergeMaps(sliceToMap(os.Environ()), sh.vars, sliceToMap(env)))
	sh.cmds = append(sh.cmds, c)
	return c
}

func (sh *shell) fn(env []string, fn *Fn, args ...interface{}) (*cmd, error) {
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

func (sh *shell) set(vars ...string) {
	for _, kv := range vars {
		k, v := splitKeyValue(kv)
		if v == "" {
			delete(sh.vars, k)
		} else {
			sh.vars[k] = v
		}
	}
}

func (sh *shell) get(name string) string {
	return sh.vars[name]
}

func (sh *shell) env() []string {
	return mapToSlice(sh.vars)
}

func (sh *shell) appendArgs(args ...string) {
	sh.args = append(sh.args, args...)
}

func (sh *shell) wait() error {
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

func (sh *shell) buildGoPkg(pkg string, flags ...string) (string, error) {
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

func (sh *shell) makeTempFile() (*os.File, error) {
	f, err := ioutil.TempFile("", "")
	if err != nil {
		return nil, err
	}
	sh.tempFiles = append(sh.tempFiles, f)
	return f, nil
}

func (sh *shell) makeTempDir() (string, error) {
	name, err := ioutil.TempDir("", "")
	if err != nil {
		return "", err
	}
	sh.tempDirs = append(sh.tempDirs, name)
	return name, nil
}

func (sh *shell) pushd(dir string) error {
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

func (sh *shell) popd() error {
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
func (sh *shell) forEachRunningChild(fn func(*os.Process)) bool {
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

func (sh *shell) cleanup() {
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
