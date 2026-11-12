// Command api is a made-up HTTP service for the flaggap examples. It reads
// its flags with the standard flag package.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"
)

const defaultListen = ":8080"

func main() {
	listen := flag.String("listen", defaultListen, "address to serve HTTP on")
	logLevel := flag.String("log-level", "info", "debug, info, warn or error")
	timeout := flag.Duration("shutdown-timeout", 20*time.Second, "how long to wait for requests on shutdown")
	workers := flag.Int("workers", 4, "number of background workers")
	debug := flag.Bool("debug", false, "serve /debug/pprof")
	var tags multiFlag
	flag.Var(&tags, "tag", "a tag to add to every log line; repeat for more")
	flag.Parse()

	fmt.Printf("listen=%s log-level=%s shutdown-timeout=%s workers=%d debug=%t tags=%v args=%q\n",
		*listen, *logLevel, *timeout, *workers, *debug, tags, flag.Args())
	if *workers < 0 {
		log.Fatal("workers can't be negative")
	}
	if flag.Arg(0) == "serve" {
		log.Fatal(http.ListenAndServe(*listen, nil))
	}
}

type multiFlag []string

func (m *multiFlag) String() string     { return fmt.Sprint(*m) }
func (m *multiFlag) Set(s string) error { *m = append(*m, s); return nil }
