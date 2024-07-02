package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, `The "livekit-cli" binary has been renamed to "lk". Try again with the new name!`)
	os.Exit(1)
}
