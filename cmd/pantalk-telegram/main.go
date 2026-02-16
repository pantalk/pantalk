package main

import (
	"os"

	"github.com/chatbotkit/pantalk/internal/client"
)

func main() {
	os.Exit(client.Run("telegram", "pantalk-telegram", os.Args[1:]))
}
