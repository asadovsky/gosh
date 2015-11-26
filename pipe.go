package gosh

import (
	"bytes"
	"errors"
	"io"
	"sync"
)

// This file implements a pipe backed by an unbounded in-memory buffer. Writes
// on the pipe never block; reads on the pipe block until data is available.
//
// References:
// https://groups.google.com/d/topic/golang-dev/k0bSal8eDyE/discussion
// https://github.com/golang/net/blob/master/http2/pipe.go
// https://github.com/vanadium/go.ref/blob/master/test/modules/queue_rw.go

type pipe struct {
	cond *sync.Cond
	buf  bytes.Buffer
	err  error
}

func newPipe() io.ReadWriteCloser {
	return &pipe{cond: sync.NewCond(&sync.Mutex{})}
}

// Read reads from the pipe.
func (p *pipe) Read(d []byte) (n int, err error) {
	p.cond.L.Lock()
	defer p.cond.L.Unlock()
	for {
		if p.buf.Len() > 0 {
			return p.buf.Read(d)
		}
		if p.err != nil {
			return 0, p.err
		}
		p.cond.Wait()
	}
}

var errWriteOnClosedPipe = errors.New("write on closed pipe")

// Write writes to the pipe.
func (p *pipe) Write(d []byte) (n int, err error) {
	p.cond.L.Lock()
	defer p.cond.L.Unlock()
	if p.err != nil {
		return 0, errWriteOnClosedPipe
	}
	defer p.cond.Signal()
	return p.buf.Write(d)
}

// Close closes the pipe.
func (p *pipe) Close() error {
	p.cond.L.Lock()
	defer p.cond.L.Unlock()
	if p.err == nil {
		defer p.cond.Signal()
		p.err = io.EOF
	}
	return nil
}
