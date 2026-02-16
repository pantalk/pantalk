package main

import (
	"os"

	"github.com/chatbotkit/pantalk/internal/client"
)

func main() {
	os.Exit(client.Run("discord", "pantalk-discord", os.Args[1:]))
}
