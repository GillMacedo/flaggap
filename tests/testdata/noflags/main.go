// Command noflags reads os.Args by hand and never uses the flag package.
package main

import (
	"fmt"
	"os"
)

func main() { fmt.Println(os.Args[1:]) }
