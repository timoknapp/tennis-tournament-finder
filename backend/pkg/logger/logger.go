package logger

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// LogLevel represents the logging level
type LogLevel int

const (
	DebugLevel LogLevel = iota
	InfoLevel
	WarnLevel
	ErrorLevel
)

// String returns the string representation of the log level
func (l LogLevel) String() string {
	switch l {
	case DebugLevel:
		return "DEBUG"
	case InfoLevel:
		return "INFO"
	case WarnLevel:
		return "WARN"
	case ErrorLevel:
		return "ERROR"
	default:
		return "INFO"
	}
}

// ParseLogLevel parses a string into a LogLevel
func ParseLogLevel(level string) LogLevel {
	switch strings.ToUpper(strings.TrimSpace(level)) {
	case "DEBUG":
		return DebugLevel
	case "INFO":
		return InfoLevel
	case "WARN", "WARNING":
		return WarnLevel
	case "ERROR":
		return ErrorLevel
	default:
		return InfoLevel
	}
}

type Logger struct {
	infoLogger  *log.Logger
	errorLogger *log.Logger
	debugLogger *log.Logger
	level       LogLevel
}

var defaultLogger *Logger

func init() {
	// Get log level from environment variable, default to INFO
	logLevelStr := os.Getenv("LOG_LEVEL")
	if logLevelStr == "" {
		logLevelStr = "INFO"
	}
	logLevel := ParseLogLevel(logLevelStr)

	defaultLogger = NewLoggerWithLevel(logLevel)
	defaultLogger.Info("Logger initialized with level: %s", logLevel.String())
}

// NewLogger creates a new logger instance with INFO level
func NewLogger() *Logger {
	return NewLoggerWithLevel(InfoLevel)
}

// NewLoggerWithLevel creates a new logger instance with specified level
func NewLoggerWithLevel(level LogLevel) *Logger {
	return &Logger{
		infoLogger:  log.New(os.Stdout, "", 0),
		errorLogger: log.New(os.Stderr, "", 0),
		debugLogger: log.New(os.Stdout, "", 0),
		level:       level,
	}
}

// SetLogLevel sets the log level for the default logger
func SetLogLevel(level LogLevel) {
	defaultLogger.level = level
	defaultLogger.Info("Log level changed to: %s", level.String())
}

// GetLogLevel returns the current log level
func GetLogLevel() LogLevel {
	return defaultLogger.level
}

// shouldLog checks if a message should be logged based on the current level
func (l *Logger) shouldLog(level LogLevel) bool {
	return level >= l.level
}

// formatMessage adds UTC timestamp prefix to the message
func (l *Logger) formatMessage(level string, message string) string {
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	return fmt.Sprintf("[%s] %s: %s", timestamp, level, message)
}

// Info logs an info message
func (l *Logger) Info(format string, args ...interface{}) {
	if l.shouldLog(InfoLevel) {
		message := fmt.Sprintf(format, args...)
		l.infoLogger.Println(l.formatMessage("INFO", message))
	}
}

// Error logs an error message
func (l *Logger) Error(format string, args ...interface{}) {
	if l.shouldLog(ErrorLevel) {
		message := fmt.Sprintf(format, args...)
		l.errorLogger.Println(l.formatMessage("ERROR", message))
	}
}

// Debug logs a debug message
func (l *Logger) Debug(format string, args ...interface{}) {
	if l.shouldLog(DebugLevel) {
		message := fmt.Sprintf(format, args...)
		l.debugLogger.Println(l.formatMessage("DEBUG", message))
	}
}

// Warn logs a warning message
func (l *Logger) Warn(format string, args ...interface{}) {
	if l.shouldLog(WarnLevel) {
		message := fmt.Sprintf(format, args...)
		l.infoLogger.Println(l.formatMessage("WARN", message))
	}
}

// Package-level convenience functions using the default logger

// Info logs an info message using the default logger
func Info(format string, args ...interface{}) {
	defaultLogger.Info(format, args...)
}

// Error logs an error message using the default logger
func Error(format string, args ...interface{}) {
	defaultLogger.Error(format, args...)
}

// Debug logs a debug message using the default logger
func Debug(format string, args ...interface{}) {
	defaultLogger.Debug(format, args...)
}

// Warn logs a warning message using the default logger
func Warn(format string, args ...interface{}) {
	defaultLogger.Warn(format, args...)
}

// Printf provides a Printf-style logging function for compatibility
func Printf(format string, args ...interface{}) {
	defaultLogger.Info(format, args...)
}

// Println provides a Println-style logging function for compatibility
func Println(args ...interface{}) {
	message := fmt.Sprint(args...)
	defaultLogger.Info("%s", message)
}

// SetLogLevelFromString sets the log level from a string (convenience function)
func SetLogLevelFromString(level string) {
	SetLogLevel(ParseLogLevel(level))
}
