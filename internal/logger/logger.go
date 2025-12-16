package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
)

// Level represents the logging level.
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
	FATAL
)

var levelNames = map[Level]string{
	DEBUG: "DEBUG",
	INFO:  "INFO",
	WARN:  "WARN",
	ERROR: "ERROR",
	FATAL: "FATAL",
}

var levelColors = map[Level]string{
	DEBUG: "\033[36m", // Cyan
	INFO:  "\033[32m", // Green
	WARN:  "\033[33m", // Yellow
	ERROR: "\033[31m", // Red
	FATAL: "\033[35m", // Magenta
}

const colorReset = "\033[0m"

// Logger is the main logger instance.
type Logger struct {
	mu          sync.Mutex
	level       Level
	output      io.Writer
	colorEnable bool
	prefix      string
}

var (
	defaultLogger *Logger
	once          sync.Once
)

// Init initializes the default logger with the specified level.
func Init(levelStr string) {
	once.Do(func() {
		level := parseLevel(levelStr)
		defaultLogger = &Logger{
			level:       level,
			output:      os.Stdout,
			colorEnable: true,
			prefix:      "",
		}
	})
}

// SetLevel sets the logging level for the default logger.
func SetLevel(levelStr string) {
	if defaultLogger == nil {
		Init(levelStr)
		return
	}
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.level = parseLevel(levelStr)
}

// SetOutput sets the output destination for the default logger.
func SetOutput(w io.Writer) {
	if defaultLogger == nil {
		Init("info")
	}
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.output = w
}

// SetColorEnable enables or disables color output.
func SetColorEnable(enable bool) {
	if defaultLogger == nil {
		Init("info")
	}
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.colorEnable = enable
}

// parseLevel converts a string to a Level.
func parseLevel(levelStr string) Level {
	switch strings.ToUpper(levelStr) {
	case "DEBUG":
		return DEBUG
	case "INFO":
		return INFO
	case "WARN", "WARNING":
		return WARN
	case "ERROR":
		return ERROR
	case "FATAL":
		return FATAL
	default:
		return INFO
	}
}

// log writes a log message if the level is sufficient.
func (l *Logger) log(level Level, format string, args ...interface{}) {
	if l == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if level < l.level {
		return
	}

	message := fmt.Sprintf(format, args...)
	levelName := levelNames[level]

	var output string
	if l.colorEnable {
		color := levelColors[level]
		output = fmt.Sprintf("%s[%s]%s %s", color, levelName, colorReset, message)
	} else {
		output = fmt.Sprintf("[%s] %s", levelName, message)
	}

	log.New(l.output, l.prefix, log.LstdFlags).Println(output)

	// Exit on FATAL
	if level == FATAL {
		os.Exit(1)
	}
}

// Debug logs a debug message.
func Debug(format string, args ...interface{}) {
	if defaultLogger == nil {
		Init("info")
	}
	defaultLogger.log(DEBUG, format, args...)
}

// Debugf is an alias for Debug.
func Debugf(format string, args ...interface{}) {
	Debug(format, args...)
}

// Info logs an info message.
func Info(format string, args ...interface{}) {
	if defaultLogger == nil {
		Init("info")
	}
	defaultLogger.log(INFO, format, args...)
}

// Infof is an alias for Info.
func Infof(format string, args ...interface{}) {
	Info(format, args...)
}

// Warn logs a warning message.
func Warn(format string, args ...interface{}) {
	if defaultLogger == nil {
		Init("info")
	}
	defaultLogger.log(WARN, format, args...)
}

// Warnf is an alias for Warn.
func Warnf(format string, args ...interface{}) {
	Warn(format, args...)
}

// Error logs an error message.
func Error(format string, args ...interface{}) {
	if defaultLogger == nil {
		Init("info")
	}
	defaultLogger.log(ERROR, format, args...)
}

// Errorf is an alias for Error.
func Errorf(format string, args ...interface{}) {
	Error(format, args...)
}

// Fatal logs a fatal message and exits the program.
func Fatal(format string, args ...interface{}) {
	if defaultLogger == nil {
		Init("info")
	}
	defaultLogger.log(FATAL, format, args...)
}

// Fatalf is an alias for Fatal.
func Fatalf(format string, args ...interface{}) {
	Fatal(format, args...)
}
