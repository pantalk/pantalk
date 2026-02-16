package main

import (
	"fmt"
	"os"

	"github.com/chatbotkit/pantalk/internal/ctl"
)

func main() {
	if err := ctl.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
