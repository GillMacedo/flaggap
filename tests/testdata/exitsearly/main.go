// Command exitsearly prints its version and exits before parsing flags.
package main

import (
	"flag"
	"fmt"
)

func main() {
	port := flag.Int("port", 80, "")
	fmt.Println("exitsearly 1.0")
	if true {
		return
	}
	flag.Parse()
	_ = port
}
