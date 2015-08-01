package main

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/asadovsky/gosh"
)

// Copied from http://golang.org/src/net/http/server.go.
type tcpKeepAliveListener struct {
	*net.TCPListener
}

func (ln tcpKeepAliveListener) Accept() (c net.Conn, err error) {
	tc, err := ln.AcceptTCP()
	if err != nil {
		return
	}
	tc.SetKeepAlive(true)
	tc.SetKeepAlivePeriod(3 * time.Minute)
	return tc, nil
}

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "Hello, world!")
	})
	srv := &http.Server{Addr: "localhost:0"}
	ln, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		panic(err)
	}
	gosh.SendVars(map[string]string{"Addr": ln.Addr().String()})
	go func() {
		time.Sleep(100 * time.Millisecond)
		gosh.SendReady()
	}()
	if err = srv.Serve(tcpKeepAliveListener{ln.(*net.TCPListener)}); err != nil {
		panic(err)
	}
}
