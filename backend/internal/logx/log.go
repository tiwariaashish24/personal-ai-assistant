package logx

import (
	"log"
	"os"
)

var Logger *log.Logger

func init() {
	f, err := os.OpenFile("jarvis.log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		Logger = log.New(os.Stderr, "jarvis ", log.LstdFlags)
		return
	}
	Logger = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
}

func Info(format string, args ...any)  { Logger.Printf("INFO  "+format, args...) }
func Error(format string, args ...any) { Logger.Printf("ERROR "+format, args...) }
func Debug(format string, args ...any) { Logger.Printf("DEBUG "+format, args...) }
