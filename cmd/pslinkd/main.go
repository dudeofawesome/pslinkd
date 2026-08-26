package main

import (
	"fmt"
	"os"

	"github.com/dudeofawesome/pslinkd/internal/command"
)

func main() {
	if err := command.Execute(os.Args[1:], os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
