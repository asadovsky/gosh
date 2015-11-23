package gosh

// This file contains functions designed to be called from a child process, e.g.
// for sending messages to the parent process. Currently, all messages are sent
// over stdout.

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const (
	msgPrefix = "#! "
	typeReady = "ready"
	typeVars  = "vars"
)

type msg struct {
	Type string
	Vars map[string]string // nil if Type is typeReady
}

func send(m msg) {
	data, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s%s\n", msgPrefix, data)
}

// SendReady tells the parent process that this child process is "ready", e.g.
// ready to serve requests.
func SendReady() {
	send(msg{Type: typeReady})
}

// SendVars sends the given vars to the parent process.
func SendVars(vars map[string]string) {
	send(msg{Type: typeVars, Vars: vars})
}

// WatchParent starts a goroutine that periodically checks whether the parent
// process has exited and, if so, kills the current process.
func WatchParent() {
	go func() {
		for {
			if os.Getppid() == 1 {
				log.Fatal("parent process has exited")
			}
			time.Sleep(time.Second)
		}
	}()
}

// OnTerminationSignal starts a goroutine that listens for various termination
// signals and calls the given function when such a signal is received.
func OnTerminationSignal(fn func(os.Signal)) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTERM)
	go func() {
		fn(<-ch)
	}()
}

// ExitOnTerminationSignal starts a goroutine that calls os.Exit(1) when a
// termination signal is received.
func ExitOnTerminationSignal() {
	OnTerminationSignal(func(os.Signal) {
		// http://www.gnu.org/software/bash/manual/html_node/Exit-Status.html
		// Unfortunately, os.Signal does not surface the signal number.
		os.Exit(1)
	})
}
