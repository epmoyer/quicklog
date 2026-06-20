// From https://gist.github.com/panta/2530672ca641d953ae452ecb5ef79d7d

package quicklog

import (
	"github.com/epmoyer/callsite"
	"fmt"
	"io"
	"os"
	"path"
	"runtime"
	"strings"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

type LogLevel int8

const defaultMaxBackups = 5
const defaultMaxSize = 50 // 50 MB
const packageVersion = "v2.0.0"

const (
	LogLevelTrace LogLevel = iota
	LogLevelDebug
	LogLevelInfo
	LogLevelError
)

var loggers = make(map[string]*LoggerT)

func (level LogLevel) String() string {
	switch level {
	case LogLevelTrace:
		return "TRACE"
	case LogLevelDebug:
		return "DEBUG"
	case LogLevelInfo:
		return "INFO"
	case LogLevelError:
		return "ERROR"
	}
	return "unknown"
}

// Configuration for logging
type ConfigT struct {
	// Unique identifier for the logger instance
	LoggerId string
	// Directory to log to to when file logging is enabled
	Directory string
	// Filename is the name of the log file which will be placed inside the directory
	Filename string
	// MaxSize the max size in MB of the log file before it's rolled
	MaxSize int
	// MaxBackups the max number of rolled files to keep
	MaxBackups int
	// MaxAge the max age in days to keep a log file
	MaxAge int
	// Min LogLevel to log
	Level LogLevel
	// Callback function to increment error counter
	FnCallbackOnError func()
	// If the logger is started with IsDisabled=true, it will not log anything, but
	// methods that both log and print (e.g. InfoPrint) will still print to the console.
	IsDisabled bool
}

func (c *ConfigT) SetDefaults() {
	if c.MaxBackups == 0 {
		c.MaxBackups = defaultMaxBackups
	}
	if c.MaxSize == 0 {
		c.MaxSize = defaultMaxSize
	}
}

type LoggerT struct {
	RollingFile       io.Writer
	Level             LogLevel
	FnCallbackOnError func()
	IsDisabled        bool
	// This is set for a "stub" logger, which gets created if someone requests a named logger
	// which does not (yet) exist.  The stub logger will "log" to stderr, and will be
	// upgraded to a real logger when ConfigureLogger is called with the same LoggerId to
	// create a real logger.
	IsStub bool
}

func GetLogger(loggerId string) *LoggerT {
	if loggerId == "" {
		panic("LoggerId must be set")
	}

	if logger, exists := loggers[loggerId]; exists {
		return logger
	}

	// Create a stub logger that logs to stderr
	stubLogger := &LoggerT{
		RollingFile:       os.Stderr,
		Level:             LogLevelInfo,
		FnCallbackOnError: nil,
		IsDisabled:        false,
		IsStub:            true,
	}
	loggers[loggerId] = stubLogger
	return stubLogger
}

func ConfigureLogger(config ConfigT) *LoggerT {
	if config.LoggerId == "" {
		panic("LoggerId must be set in the ConfigT")
	}
	config.SetDefaults()
	rollingFile := newRollingFile(config)

	// This will return the stub logger if it exists, or create a new stub logger.
	// If it return a REAL logger, then that means a logger has ALREADY been configured
	// with the same LoggerId, which is not allowed, so we panic.
	logger := GetLogger(config.LoggerId)
	if !logger.IsStub {
		panic(fmt.Sprintf("Logger with LoggerId '%s' has already been configured", config.LoggerId))
	}

	// Upgrade the stub logger to a real logger
	logger.RollingFile = rollingFile
	logger.Level = config.Level
	logger.FnCallbackOnError = config.FnCallbackOnError
	logger.IsDisabled = config.IsDisabled
	logger.IsStub = false

	logger.Info("---------------------------- BEGIN ----------------------------")
	logger.Infof("quicklog %s", packageVersion)
	return logger
}

func (l LoggerT) Trace() {
	// msg := getFunctionNameOfCaller() + "()"
	msg := getIndentedFunctionNameOfCaller()
	l.CreateLogEntry(msg, LogLevelTrace)
}

func (l LoggerT) Debug(msg string) {
	l.CreateLogEntry(msg, LogLevelDebug)
}

func (l LoggerT) Debugf(format string, a ...interface{}) {
	l.CreateLogEntry(fmt.Sprintf(format, a...), LogLevelDebug)
}

func (l LoggerT) Info(msg string) {
	l.CreateLogEntry(msg, LogLevelInfo)
}

func (l LoggerT) Infof(format string, a ...interface{}) {
	l.CreateLogEntry(fmt.Sprintf(format, a...), LogLevelInfo)
}

func (l LoggerT) InfoPrint(msg string) {
	fmt.Println(msg)
	l.Info(msg)
}

func (l LoggerT) InfoPrintf(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	l.InfoPrint(msg)
}

func (l LoggerT) Error(msg string) {
	l.CreateLogEntry(msg, LogLevelError)
	if l.FnCallbackOnError != nil {
		l.FnCallbackOnError()
	}
}

func (l LoggerT) Errorf(format string, a ...interface{}) {
	l.Error(fmt.Sprintf(format, a...))
}

func (l LoggerT) ErrorfContext(_format string, a ...interface{}) {
	l.Error(callsite.SprintfContext(_format, a...))
}

func (l LoggerT) ErrorE(err error) {
	l.Error(err.Error())
}

func (l LoggerT) ErrorPrint(msg string) {
	fmt.Println("ERROR: " + msg)
	l.Error(msg)
}

func (l LoggerT) ErrorPrintf(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	l.ErrorPrint(msg)
}

func (l LoggerT) CreateLogEntry(msg string, level LogLevel) {
	if level < l.Level || l.IsDisabled {
		return
	}

	// TODO: Use the pattern below to implement an option allowing
	//       a timezone location to be specified (e.g., to log as UTC)
	// ----------------------------------------------------------------
	// location, _ := time.LoadLocation("America/New_York")
	// now := time.Now().In(location)
	// ----------------------------------------------------------------

	timestamp := time.Now().Format("2006-01-02T15:04:05.000-0700")
	l.RollingFile.Write([]byte(fmt.Sprintf("%s | %-5s | %s\n", timestamp, level.String(), msg)))
}

func newRollingFile(config ConfigT) io.Writer {
	if err := os.MkdirAll(config.Directory, 0744); err != nil {
		fmt.Printf("Can't create log directory at: %s\n", config.Directory)
		return nil
	}

	return &lumberjack.Logger{
		Filename:   path.Join(config.Directory, config.Filename),
		MaxBackups: config.MaxBackups, // files
		MaxSize:    config.MaxSize,    // megabytes
		MaxAge:     config.MaxAge,     // days
	}
}

// getFunctionNameOfCaller gets the name of the function which CALLED the function
// which called getFunctionNameOfCaller.
// func getFunctionNameOfCaller() string {
// 	pc, _, _, ok := runtime.Caller(2) // 1 to get the calling function
// 	if !ok {
// 		return "unknown"
// 	}
// 	return runtime.FuncForPC(pc).Name()
// }

func getIndentedFunctionNameOfCaller() string {

	// Start with skip = 2 to get the CALLER of the function that CALLS this function.
	const depthOfCaller = 2

	skip := depthOfCaller
	// fmt.Printf("🟣  trace\n")
	prefix := ""
	for {
		pc, _, _, ok := runtime.Caller(skip)
		if !ok {
			break
		}
		name := runtime.FuncForPC(pc).Name()
		// fmt.Printf("🟣      %s\n", name)
		if name == "runtime.main" {
			// We stop traversing the stack at runtime.main because we consider that
			// "Depth -1" for our purposes.  In practice the Go runtime has an additional
			// two items on the stack which we will be ignoring (runtime.main, runtime.goexit).
			// We decrement skip to get back to the "Depth 0" count.
			skip--
			break
		}
		if name == "github.com/gin-gonic/gin.(*Context).Next" {
			// During "primary" operation all execution happens as route callbacks from Gin.
			// We will prefix those callbacks with "gin:", and consider that caller to be
			// to be "Depth -1" for our purposes.
			// We decrement skip to get back to the "Depth 0" count.
			prefix = "gin:"
			skip--
			break
		}
		skip++
	}

	// We clip to 0 so that the value is never negative if we somehow made a mistake.
	depth := max(skip-depthOfCaller, 0)

	// Get the function name of the caller
	pc, _, _, ok := runtime.Caller(2)
	if !ok {
		return "unknown"
	}
	funcName := runtime.FuncForPC(pc).Name()

	// Create indentation based on the depth
	indentation := strings.Repeat("  ", depth)

	return fmt.Sprintf("%s%s%s()", prefix, indentation, funcName)
}
