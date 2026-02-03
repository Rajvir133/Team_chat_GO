package config

import(
	"log"
	)

func DebugLog(format string, v ...interface{}) {
	if DebugMode {
		log.Printf("[DEBUG] "+format, v...)
	}
}

// InfoLog prints essential logs (always visible)
func InfoLog(format string, v ...interface{}) {
	log.Printf("[INFO]  "+format, v...)
}

// ErrorLog prints error logs (always visible)
func ErrorLog(format string, v ...interface{}) {
	log.Printf("[ERROR] "+format, v...)
}

