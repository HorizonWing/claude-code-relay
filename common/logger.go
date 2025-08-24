package common

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

var (
	InfoLogger  *log.Logger
	ErrorLogger *log.Logger
)

func SetupLogger() {
	logDir := "./logs"
	if err := os.MkdirAll(logDir, 0755); err != nil {
		log.Printf("创建日志目录失败，将只输出到控制台: %v", err)
		return
	}

	logFile := filepath.Join(logDir, "app.log")
	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		log.Printf("打开日志文件失败，将只输出到控制台: %v", err)
		return
	}

	InfoLogger = log.New(file, "[INFO] ", log.Ldate|log.Ltime|log.Lshortfile)
	ErrorLogger = log.New(file, "[ERROR] ", log.Ldate|log.Ltime|log.Lshortfile)
	log.Println("日志系统初始化成功，日志将同时输出到控制台和文件")
}

func SysLog(message string) {
	logMessage := fmt.Sprintf("[SYSTEM] %s", message)
	log.Println(logMessage)
	if InfoLogger != nil {
		InfoLogger.Println(message)
	}
}

func SysError(message string) {
	logMessage := fmt.Sprintf("[ERROR] %s", message)
	log.Println(logMessage)
	if ErrorLogger != nil {
		ErrorLogger.Println(message)
	}
}

func FatalLog(message string) {
	SysError(message)
	os.Exit(1)
}