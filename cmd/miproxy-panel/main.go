package main

import (
	"fmt"
	"github.com/MaoLikeQvQ/MiProxy-Panel/internal/app"
	"os"
)

func main() {
	if err := app.Run(os.Args[0], os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "MiProxy:", err)
		os.Exit(1)
	}
}
