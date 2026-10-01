package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"klog/internal/web"
)

// checkWeb validates the --web flags. It runs before any kubectl call.
func checkWeb(fs *flag.FlagSet, on bool, addr, out string) error {
	if !on {
		if addr != "" {
			return errors.New("--web-addr needs --web")
		}
		return nil
	}
	if out != "" {
		return errors.New("--web cannot be combined with --out")
	}
	if flagSet(fs, "format") || flagSet(fs, "template") {
		return errors.New("--web cannot be combined with --format or --template (the browser always gets JSON records)")
	}
	if addr == "" {
		return nil
	}
	return web.CheckAddr(addr)
}

// startWeb serves a new hub on addr (a free loopback port when empty) and
// prints its URL. stop shuts the server down.
func startWeb(addr string, stderr io.Writer) (hub *web.Hub, stop func(), err error) {
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	hub = web.NewHub(web.RingSize, web.QueueSize)
	hub.Rebase(uint64(time.Now().UnixNano())) // ids stay above any earlier run's, so a reconnecting page sees a gap
	srv, err := web.Start(addr, hub)
	if err != nil {
		return nil, nil, err
	}
	fmt.Fprintf(stderr, "klog: web UI at %s\n", srv.URL())
	return hub, srv.Close, nil
}
