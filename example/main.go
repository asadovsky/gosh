package main

import (
	"fmt"

	"github.com/asadovsky/gosh"
	"github.com/asadovsky/gosh/example/lib"
)

func ExampleCmds() {
	sh := gosh.NewShell(gosh.ShellOpts{SuppressChildOutput: true})
	defer sh.Cleanup()

	// Start server.
	binPath := sh.BuildGoPkg("github.com/asadovsky/gosh/example/server")
	c := sh.Cmd(binPath)
	c.Start()
	c.AwaitReady()
	addr := c.AwaitVars("Addr")["Addr"]
	fmt.Println(addr)

	// Run client.
	binPath = sh.BuildGoPkg("github.com/asadovsky/gosh/example/client")
	c = sh.Cmd(binPath, "-addr="+addr)
	stdout, _ := c.Output()
	fmt.Print(string(stdout))
}

var (
	get   = gosh.Register("get", lib.Get)
	serve = gosh.Register("serve", lib.Serve)
)

func ExampleFns() {
	sh := gosh.NewShell(gosh.ShellOpts{SuppressChildOutput: true})
	defer sh.Cleanup()

	// Start server.
	c := sh.Fn(serve)
	c.Start()
	c.AwaitReady()
	addr := c.AwaitVars("Addr")["Addr"]
	fmt.Println(addr)

	// Run client.
	c = sh.Fn(get, addr)
	stdout, _ := c.Output()
	fmt.Print(string(stdout))
}

func ExampleShellMain() {
	sh := gosh.NewShell(gosh.ShellOpts{})
	defer sh.Cleanup()
	stdout, _ := sh.Main(lib.HelloWorldMain).Output()
	fmt.Print(string(stdout))
}

func main() {
	gosh.MaybeRunFnAndExit()
	ExampleCmds()
	ExampleFns()
	ExampleShellMain()
}
