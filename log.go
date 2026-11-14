package main

import (
	"log"
	"os"
)

var logger *log.Logger

func InitLogger() {
	f, err := os.OpenFile("assistant.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		logger = log.New(os.Stderr, "assistant ", log.LstdFlags)
		return
	}
	logger = log.New(f, "", log.LstdFlags)
}

func Logf(format string, args ...any) {
	if logger != nil {
		logger.Printf(format, args...)
	}
}
