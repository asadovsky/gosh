package gosh

// This file contains functions designed to be called from a child process, e.g.
// for sending messages to the parent process. Currently, all messages are sent
// over stdout.

// SendVars sends the given vars to the parent process.
func SendVars(vars map[string]string) {
	// FIXME
}

// SendReady tells the parent process that this child process is "ready", e.g.
// ready to serve requests.
func SendReady() {
	// FIXME
}

// WatchParent starts a goroutine that periodically checks whether the parent
// process has exited and, if so, kills this process.
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
