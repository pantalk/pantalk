package main

import (
	"os"

	"github.com/chatbotkit/pantalk/internal/client"
)

func main() {
	os.Exit(client.Run("slack", "pantalk-slack", os.Args[1:]))
}
