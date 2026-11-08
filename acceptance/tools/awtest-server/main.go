// Command awtest-server is a server for scenarios and benchmarks: it answers HTTP on a port with its
// environment's AW_LANE, and appends a line to -sigrec when it receives SIGTERM.
//
//	awtest-server -port 41000 [-sigrec file] [-started file]
package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

func main() {
	port := flag.Int("port", 0, "the port to listen on")
	sigrec := flag.String("sigrec", "", "append the time here on SIGTERM")
	started := flag.String("started", "", "append the time here once listening")
	flag.Parse()
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(*port)))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	now := func() string { return strconv.FormatInt(time.Now().UnixNano(), 10) }
	if *started != "" {
		appendLine(*started, now())
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM)
	go func() {
		<-sigs
		if *sigrec != "" {
			appendLine(*sigrec, now())
		}
		os.Exit(0)
	}()
	http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "lane=%s\n", os.Getenv("AW_LANE"))
	}))
}
