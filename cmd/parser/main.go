package main

import (
	"fmt"
	"os"

	"github.com/djohts/maps-go/internal/parser"
)

func main() {
	if err := parser.Run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
