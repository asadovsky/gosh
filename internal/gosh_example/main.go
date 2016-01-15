// Copyright 2015 The Vanadium Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"

	"github.com/asadovsky/gosh"
	"github.com/asadovsky/gosh/internal/gosh_example_lib"
)

func ExampleCmd() {
	sh := gosh.NewShell(gosh.Opts{})
	defer sh.Cleanup()

	// Start server.
	binPath := sh.BuildGoPkg("github.com/asadovsky/gosh/internal/gosh_example_server")
	c := sh.Cmd(binPath)
	c.Start()
	c.AwaitReady()
	addr := c.AwaitVars("Addr")["Addr"]
	fmt.Println(addr)

	// Run client.
	binPath = sh.BuildGoPkg("github.com/asadovsky/gosh/internal/gosh_example_client")
	c = sh.Cmd(binPath, "-addr="+addr)
	fmt.Print(c.Stdout())
}

var (
	getFunc   = gosh.RegisterFunc("getFunc", lib.Get)
	serveFunc = gosh.RegisterFunc("serveFunc", lib.Serve)
)

func ExampleFuncCmd() {
	sh := gosh.NewShell(gosh.Opts{})
	defer sh.Cleanup()

	// Start server.
	c := sh.FuncCmd(serveFunc)
	c.Start()
	c.AwaitReady()
	addr := c.AwaitVars("Addr")["Addr"]
	fmt.Println(addr)

	// Run client.
	c = sh.FuncCmd(getFunc, addr)
	fmt.Print(c.Stdout())
}

func main() {
	gosh.InitMain()
	ExampleCmd()
	ExampleFuncCmd()
}
