package main

import (
	"log"
	"os"

	"sencia.job/backend/internal/server"
)

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags|log.LUTC)
	if err := server.Run(logger); err != nil {
		logger.Fatal(err)
	}
}
