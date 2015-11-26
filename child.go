package gosh

// This file contains functions designed to be called from a child process, e.g.
// for sending messages to the parent process. Currently, all messages are sent
// over stdout.

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
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
