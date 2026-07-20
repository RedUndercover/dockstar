package main

import (
	"fmt"
	"os"

	"github.com/redundercover/dockstar/internal/wake"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: dockstar-wake-helper enable|disable|apply|restore")
		os.Exit(2)
	}
	if err := wake.Helper(os.Args[1], ""); err != nil {
		fmt.Fprintln(os.Stderr, "dockstar-wake-helper:", err)
		os.Exit(1)
	}
}
