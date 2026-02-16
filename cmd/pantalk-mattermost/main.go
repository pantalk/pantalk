package main

import (
	"os"

	"github.com/chatbotkit/pantalk/internal/client"
)

func main() {
	os.Exit(client.Run("mattermost", "pantalk-mattermost", os.Args[1:]))
}
