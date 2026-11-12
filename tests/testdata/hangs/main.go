// Command hangs waits forever before it parses its flags.
package main

import (
	"flag"
	"time"
)

func main() {
	port := flag.Int("port", 80, "")
	time.Sleep(time.Hour)
	flag.Parse()
	_ = port
}
