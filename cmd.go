package gosh

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

var (
	errAlreadyCalledStart = errors.New("already called start")
	errAlreadyCalledWait  = errors.New("already called wait")
	errNotStarted         = errors.New("not started")
)

// Cmd represents a command. Not thread-safe.
// Opts, Vars, and Args should not be modified after calling Start.
type Cmd struct {
	// Opts is the CmdOpts for this Cmd.
	Opts CmdOpts
	// Vars is the map of env vars for this Cmd.
	Vars map[string]string
	// Args is the list of args for this Cmd.
	Args []string
	// Internal state.
	sh             *Shell
	c              *exec.Cmd
	name           string
	calledWait     bool
	stdoutWriters  []io.Writer
	stderrWriters  []io.Writer
	closeAfterWait []io.Closer
	condReady      *sync.Cond
	ready          bool // protected by condReady.L
	condVars       *sync.Cond
	vars           map[string]string // protected by condVars.L
}

// CmdOpts configures Cmd. See ShellOpts for field descriptions.
type CmdOpts struct {
	SuppressOutput bool
	OutputDir      string
}

// Stdout returns a Reader backed by a buffered pipe for this command's stdout.
// Must be called before Start. May be called more than once; each invocation
// creates a new pipe.
func (c *Cmd) Stdout() io.Reader {
	c.sh.ok()
	res, err := c.stdout()
	c.sh.SetErr(err)
	return res
}

// Stderr returns a Reader backed by a buffered pipe for this command's stderr.
// Must be called before Start. May be called more than once; each invocation
// creates a new pipe.
func (c *Cmd) Stderr() io.Reader {
	c.sh.ok()
	res, err := c.stderr()
	c.sh.SetErr(err)
	return res
}

// Start starts this command.
func (c *Cmd) Start() {
	c.sh.ok()
	c.sh.SetErr(c.start())
}

// AwaitReady waits for the child process to call SendReady. Must not be called
// before Start or after Wait.
func (c *Cmd) AwaitReady() {
	c.sh.ok()
	c.sh.SetErr(c.awaitReady())
}

// AwaitVars waits for the child process to send values for the given vars
// (using SendVars). Must not be called before Start or after Wait.
func (c *Cmd) AwaitVars(keys ...string) map[string]string {
	c.sh.ok()
	res, err := c.awaitVars(keys...)
	c.sh.SetErr(err)
	return res
}

// Wait waits for this command to exit.
func (c *Cmd) Wait() {
	c.sh.ok()
	c.sh.SetErr(c.wait())
}

// TODO: Maybe add a method to send SIGINT, wait for a bit, then send SIGKILL if
// the process hasn't exited.

// Shutdown sends the given signal to this command, then waits for it to exit.
func (c *Cmd) Shutdown(sig os.Signal) {
	c.sh.ok()
	c.sh.SetErr(c.shutdown(sig))
}

// Run calls Start followed by Wait.
func (c *Cmd) Run() {
	c.sh.ok()
	c.sh.SetErr(c.run())
}

// Output calls Start followed by Wait, then returns this command's stdout and
// stderr.
func (c *Cmd) Output() ([]byte, []byte) {
	c.sh.ok()
	stdout, stderr, err := c.output()
	c.sh.SetErr(err)
	return stdout, stderr
}

// CombinedOutput calls Start followed by Wait, then returns this command's
// combined stdout and stderr.
func (c *Cmd) CombinedOutput() []byte {
	c.sh.ok()
	res, err := c.combinedOutput()
	c.sh.SetErr(err)
	return res
}

// Process returns the underlying process handle for this command.
func (c *Cmd) Process() *os.Process {
	c.sh.ok()
	res, err := c.process()
	c.sh.SetErr(err)
	return res
}

////////////////////////////////////////
// Internals

func newCmd(sh *Shell, opts CmdOpts, vars map[string]string, name string, args ...string) (*Cmd, error) {
	// Mimics https://golang.org/src/os/exec/exec.go Command.
	if filepath.Base(name) == name {
		if lp, err := exec.LookPath(name); err != nil {
			return nil, err
		} else {
			name = lp
		}
	}
	c := &Cmd{
		Opts:           opts,
		Vars:           vars,
		Args:           args,
		sh:             sh,
		name:           name,
		stdoutWriters:  []io.Writer{},
		stderrWriters:  []io.Writer{},
		closeAfterWait: []io.Closer{},
		condReady:      sync.NewCond(&sync.Mutex{}),
		condVars:       sync.NewCond(&sync.Mutex{}),
		vars:           map[string]string{},
	}
	sh.cmds = append(sh.cmds, c)
	return c, nil
}

func (c *Cmd) calledStart() bool {
	return c.c != nil
}

func closeAll(closers []io.Closer) {
	for _, c := range closers {
		c.Close()
	}
}

func addWriter(writers *[]io.Writer, w io.Writer) {
	*writers = append(*writers, w)
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
	if !c.Opts.SuppressOutput {
		addWriter(writers, f)
	}
	if c.Opts.OutputDir != "" {
		suffix := "stderr"
		if f == os.Stdout {
			suffix = "stdout"
		}
		name := filepath.Join(c.Opts.OutputDir, filepath.Base(c.name)+"."+t+"."+suffix)
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		addWriter(writers, f)
		c.closeAfterWait = append(c.closeAfterWait, f)
	}
	if f == os.Stdout {
		addWriter(writers, &recvWriter{c: c})
	}
	return io.MultiWriter(*writers...), nil
}

func (c *Cmd) stdout() (io.Reader, error) {
	if c.calledStart() {
		return nil, errAlreadyCalledStart
	}
	p := NewBufferedPipe()
	addWriter(&c.stdoutWriters, p)
	c.closeAfterWait = append(c.closeAfterWait, p)
	return p, nil
}

func (c *Cmd) stderr() (io.Reader, error) {
	if c.calledStart() {
		return nil, errAlreadyCalledStart
	}
	p := NewBufferedPipe()
	addWriter(&c.stderrWriters, p)
	c.closeAfterWait = append(c.closeAfterWait, p)
	return p, nil
}

func (c *Cmd) start() error {
	if c.calledStart() {
		return errAlreadyCalledStart
	}
	c.c = exec.Command(c.name, c.Args...)
	c.c.Env = mapToSlice(c.Vars)
	if c.c.Stdout != nil || c.c.Stderr != nil { // invariant check
		log.Fatal(c.c.Stdout, c.c.Stderr)
	}
	// Set up stdout and stderr.
	t := time.Now().UTC().Format("20060102.150405.000000")
	var err error
	if c.c.Stdout, err = c.initMultiWriter(os.Stdout, t); err != nil {
		return err
	}
	if c.c.Stderr, err = c.initMultiWriter(os.Stderr, t); err != nil {
		return err
	}
	// TODO: Maybe wrap every child process with a "supervisor" process that calls
	// WatchParent().
	err = c.c.Start()
	if err != nil {
		closeAll(c.closeAfterWait)
	}
	return err
}

// TODO: Add timeouts for Cmd.{awaitReady,awaitVars,wait}.

func (c *Cmd) awaitReady() error {
	if !c.calledStart() {
		return errNotStarted
	} else if c.calledWait {
		return errAlreadyCalledWait
	}
	// http://golang.org/pkg/sync/#Cond.Wait
	c.condReady.L.Lock()
	for !c.ready {
		c.condReady.Wait()
	}
	c.condReady.L.Unlock()
	return nil
}

func (c *Cmd) awaitVars(keys ...string) (map[string]string, error) {
	if !c.calledStart() {
		return nil, errNotStarted
	} else if c.calledWait {
		return nil, errAlreadyCalledWait
	}
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
	if !c.calledStart() {
		return errNotStarted
	} else if c.calledWait {
		return errAlreadyCalledWait
	}
	c.calledWait = true
	err := c.c.Wait()
	closeAll(c.closeAfterWait)
	return err
}

func (c *Cmd) shutdown(sig os.Signal) error {
	if !c.calledStart() {
		return errNotStarted
	}
	if err := c.c.Process.Signal(sig); err != nil {
		return err
	}
	if err := c.wait(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			return err
		}
	}
	return nil
}

func (c *Cmd) run() error {
	if err := c.start(); err != nil {
		return err
	}
	return c.wait()
}

func (c *Cmd) output() ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	addWriter(&c.stdoutWriters, &stdout)
	addWriter(&c.stderrWriters, &stderr)
	err := c.run()
	return stdout.Bytes(), stderr.Bytes(), err
}

type threadSafeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *threadSafeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *threadSafeBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Bytes()
}

func (c *Cmd) combinedOutput() ([]byte, error) {
	buf := &threadSafeBuffer{}
	addWriter(&c.stdoutWriters, buf)
	addWriter(&c.stderrWriters, buf)
	err := c.run()
	return buf.Bytes(), err
}

func (c *Cmd) process() (*os.Process, error) {
	if !c.calledStart() {
		return nil, errNotStarted
	}
	return c.c.Process, nil
}
