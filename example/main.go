// Assumes the parent dir of github.com/asadovsky/gosh is in GOPATH.

package main

import (
	"fmt"

	"github.com/asadovsky/gosh"
)

func ExampleHello() {
	sh := gosh.NewShell(gosh.ShellOpts{})
	defer sh.Cleanup()

	// Start server.
	binPath := sh.BuildGoPkg("github.com/asadovsky/gosh/hello_server")
	c := sh.Cmd(nil, binPath)
	c.Start()
	c.AwaitReady()
	addr := c.AwaitVars("Addr")["Addr"]
	fmt.Println(addr)

	// Run client.
	binPath = sh.BuildGoPkg("github.com/asadovsky/gosh/hello_client")
	c = sh.Cmd(nil, binPath, "-addr="+addr)
	output := string(c.Output())
	fmt.Print(output)
}

func main() {
	ExampleHello()
}
