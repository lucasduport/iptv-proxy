package utils

import (
	"fmt"
	"log"
	"os"
)

// Config holds the application configuration
var Config = struct {
	DebugLoggingEnabled bool
}{
	DebugLoggingEnabled: os.Getenv("DEBUG_LOGGING") == "true", // Initialize from environment
}

// DebugLog logs a message if debug logging is enabled
func DebugLog(format string, args ...interface{}) {
	if Config.DebugLoggingEnabled {
		log.Printf("[DEBUG] "+format, args...)
	}
}

// InfoLog logs an informational message
func InfoLog(format string, args ...interface{}) {
	log.Printf("[INFO] "+format, args...)
}

// ErrorLog logs an error message
func ErrorLog(format string, args ...interface{}) {
	log.Printf("[ERROR] "+format, args...)
}

// FatalLog logs a fatal error message and exits the program
func FatalLog(format string, args ...interface{}) {
	log.Fatalf("[FATAL] "+format, args...)
}

// PrintEnv prints the current environment variables
func PrintEnv() {
	for _, e := range os.Environ() {
		fmt.Println(e)
	}
}