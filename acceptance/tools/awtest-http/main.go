// Command awtest-http fetches http://<address>/ once, prints the body, and exits 1 if it cannot.
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	c := http.Client{Timeout: 2 * time.Second}
	r, err := c.Get("http://" + os.Args[1] + "/")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer r.Body.Close()
	io.Copy(os.Stdout, r.Body)
}
