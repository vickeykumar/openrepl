package utils

import (
	"github.com/natefinch/lumberjack"
	"log"
	"os"
)

const LOG_PATH = "/gottyTraces"

func InitLogging(name string) {
	err := os.MkdirAll(LOG_PATH, 0755)
	if err == nil {
		log.Printf("Writting gotty logs in : " + LOG_PATH + "/" + name + ".log")
		log.SetOutput(&lumberjack.Logger{
			Filename:   LOG_PATH + "/" + name + ".log",
			MaxSize:    10, // megabytes
			MaxBackups: 5,
			MaxAge:     30, //days
		})
		log.Printf("Writting gotty logs in : " + LOG_PATH + "/" + name + ".log")
	} else {
		log.Printf("Error: unable to create log directory: " + LOG_PATH)
		os.Exit(3)
	}
}
