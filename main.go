package main

import (
	"context"
	"fmt"
	"os"

	"github.com/zolstein/monoslices/internal/app"
)

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "monoslices: cannot determine working directory: %v\n", err)
		os.Exit(1)
	}
	os.Exit(app.Run(context.Background(), cwd, os.Args[1:], os.Stdout, os.Stderr))
}
