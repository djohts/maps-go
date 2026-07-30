package main

import (
	"fmt"
	"os"

	"github.com/djohts/maps-go/internal/generator"
)

func main() {
	if err := generator.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
