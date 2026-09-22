package main

import (
	"fmt"
	"os"

	"github.com/redundercover/dockstar/internal/startup"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: dockstar-startup-helper enable|disable|sync")
		os.Exit(2)
	}
	if err := startup.Helper(os.Args[1], os.Stdin); err != nil {
		fmt.Fprintln(os.Stderr, "dockstar-startup-helper:", err)
		os.Exit(1)
	}
}
