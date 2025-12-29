package logger

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
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
	console     io.Writer // Console output (with color)
	file        io.Writer // File output (without color)
	fileHandle  *os.File  // Keep file handle for closing
	colorEnable bool
	prefix      string
}

var (
	defaultLogger *Logger
	once          sync.Once
)

// Init initializes the default logger with the specified level (console only).
func Init(levelStr string) {
	once.Do(func() {
		level := parseLevel(levelStr)
		defaultLogger = &Logger{
			level:       level,
			console:     os.Stdout,
			file:        nil,
			colorEnable: true,
			prefix:      "",
		}
	})
}

// InitWithFile initializes the logger with both console and file output.
// The log file is created in logDir with a timestamp-based name: YYYY-MM-DD_HH-MM-SS_TZ.log
// Console output includes colors, file output does not.
func InitWithFile(levelStr string, logDir string) error {
	level := parseLevel(levelStr)

	// Create log directory if it doesn't exist
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// Generate log filename with timestamp and timezone
	now := time.Now()
	zone, _ := now.Zone()
	filename := fmt.Sprintf("%s_%s.log", now.Format("2006-01-02_15-04-05"), zone)
	logPath := filepath.Join(logDir, filename)

	// Open log file
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	once.Do(func() {
		defaultLogger = &Logger{
			level:       level,
			console:     os.Stdout,
			file:        file,
			fileHandle:  file,
			colorEnable: true,
			prefix:      "",
		}
	})

	// If already initialized, update the existing logger
	if defaultLogger.file == nil {
		defaultLogger.mu.Lock()
		defaultLogger.file = file
		defaultLogger.fileHandle = file
		defaultLogger.level = level
		defaultLogger.mu.Unlock()
	}

	Info("Log file: %s", logPath)
	return nil
}

// Close closes the log file if open.
func Close() {
	if defaultLogger != nil && defaultLogger.fileHandle != nil {
		defaultLogger.mu.Lock()
		defaultLogger.fileHandle.Close()
		defaultLogger.fileHandle = nil
		defaultLogger.file = nil
		defaultLogger.mu.Unlock()
	}
}

// GetLogFilePath returns the current log file path, or empty string if no file logging.
func GetLogFilePath() string {
	if defaultLogger != nil && defaultLogger.fileHandle != nil {
		return defaultLogger.fileHandle.Name()
	}
	return ""
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

// SetOutput sets the console output destination for the default logger.
func SetOutput(w io.Writer) {
	if defaultLogger == nil {
		Init("info")
	}
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.console = w
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

	// Write to console with color
	if l.console != nil {
		var consoleOutput string
		if l.colorEnable {
			color := levelColors[level]
			consoleOutput = fmt.Sprintf("%s[%s]%s %s", color, levelName, colorReset, message)
		} else {
			consoleOutput = fmt.Sprintf("[%s] %s", levelName, message)
		}
		log.New(l.console, l.prefix, log.LstdFlags).Println(consoleOutput)
	}

	// Write to file without color
	if l.file != nil {
		fileOutput := fmt.Sprintf("[%s] %s", levelName, message)
		log.New(l.file, l.prefix, log.LstdFlags).Println(fileOutput)
	}

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
