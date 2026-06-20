// From https://gist.github.com/panta/2530672ca641d953ae452ecb5ef79d7d

package quicklog

import (
	"errors"
	"fmt"
	"github.com/epmoyer/callsite"
	"io"
	"os"
	"path"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

type LogLevel int8

const defaultMaxBackups = 5
const defaultMaxSize = 50 // 50 MB
const modulePath = "github.com/epmoyer/quicklog/v2"

const (
	LogLevelTrace LogLevel = iota
	LogLevelDebug
	LogLevelInfo
	LogLevelError
)

var (
	loggers   = make(map[string]*LoggerT)
	loggersMu sync.Mutex
)

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
	// MaxAge the max age in days to keep a rolled log file. Unlike MaxSize and
	// MaxBackups, this has no default: if left 0, lumberjack performs no
	// age-based deletion (the file count is still bounded by MaxBackups).
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
	// NOTE: MaxAge is intentionally not defaulted. Leaving it 0 disables
	// age-based deletion in lumberjack; the number of rolled files is still
	// bounded by MaxBackups.
}

type LoggerT struct {
	// LoggerId is the registry id this logger was created under. Used in
	// diagnostic messages.
	LoggerId          string
	RollingFile       io.Writer
	Level             LogLevel
	FnCallbackOnError func()
	IsDisabled        bool
	// This is set for a "stub" logger, which gets created if someone requests a named logger
	// which does not (yet) exist.  The stub logger will "log" to stderr, and will be
	// upgraded to a real logger when ConfigureLogger is called with the same LoggerId to
	// create a real logger.
	IsStub bool
	// IsLogWriteFailedOnce records whether a write to RollingFile has already
	// failed. We surface the first failure on stderr but stay quiet on every
	// subsequent one so a broken sink doesn't spam. atomic.Bool keeps it safe
	// under concurrent writes; methods use pointer receivers so it is never
	// copied.
	IsLogWriteFailedOnce atomic.Bool
	// IsStubWarnedOnce records whether we have already warned that this logger is
	// an unconfigured stub still writing to stderr. Warned once, then silent, in
	// the same spirit as IsLogWriteFailedOnce.
	IsStubWarnedOnce atomic.Bool
}

func GetLogger(loggerId string) *LoggerT {
	if loggerId == "" {
		panic("LoggerId must be set")
	}

	loggersMu.Lock()
	defer loggersMu.Unlock()
	return getLoggerLocked(loggerId)
}

// getLoggerLocked returns the logger registered for loggerId, lazily creating a
// stderr stub if none exists yet. Callers MUST hold loggersMu.
func getLoggerLocked(loggerId string) *LoggerT {
	if logger, exists := loggers[loggerId]; exists {
		return logger
	}

	// Create a stub logger that logs to stderr
	stubLogger := &LoggerT{
		LoggerId:          loggerId,
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

	// Fetch-and-upgrade must happen atomically under the lock; otherwise two
	// goroutines configuring the same LoggerId could both observe the stub and
	// both "upgrade" it, racing on the fields and defeating the double-configure
	// guard below.
	loggersMu.Lock()
	// getLoggerLocked returns the stub logger if it exists, or creates a new stub
	// logger. If it returns a REAL logger, then a logger has ALREADY been
	// configured with the same LoggerId, which is not allowed, so we panic.
	logger := getLoggerLocked(config.LoggerId)
	if !logger.IsStub {
		loggersMu.Unlock()
		panic(fmt.Sprintf("Logger with LoggerId '%s' has already been configured", config.LoggerId))
	}

	// Upgrade the stub logger to a real logger
	logger.RollingFile = rollingFile
	logger.Level = config.Level
	logger.FnCallbackOnError = config.FnCallbackOnError
	logger.IsDisabled = config.IsDisabled
	logger.IsStub = false
	loggersMu.Unlock()

	logger.Info("---------------------------- BEGIN ----------------------------")
	logger.Infof("quicklog %s", packageVersion())
	return logger
}

// Close flushes and closes the logger registered under loggerId and removes it
// from the registry. After Close, a subsequent GetLogger(loggerId) returns a
// fresh stub and ConfigureLogger may register a new logger under that id. It is
// a no-op (returning nil) if no logger is registered under loggerId. The shared
// stderr/stdout streams used by stub loggers are never closed.
func Close(loggerId string) error {
	loggersMu.Lock()
	defer loggersMu.Unlock()

	logger, exists := loggers[loggerId]
	if !exists {
		return nil
	}
	delete(loggers, loggerId)
	return closeWriter(logger.RollingFile)
}

// CloseAll closes every registered logger and empties the registry, returning
// the joined error of all underlying Close calls. It is typically called once
// on program shutdown.
func CloseAll() error {
	loggersMu.Lock()
	defer loggersMu.Unlock()

	var errs []error
	for id, logger := range loggers {
		if err := closeWriter(logger.RollingFile); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", id, err))
		}
		delete(loggers, id)
	}
	return errors.Join(errs...)
}

// closeWriter closes w if it owns a closeable resource (e.g. a lumberjack file).
// It never closes the shared std streams, which stub loggers and the
// mkdir-failure fallback write to.
func closeWriter(w io.Writer) error {
	if w == os.Stderr || w == os.Stdout {
		return nil
	}
	if c, ok := w.(io.Closer); ok {
		return c.Close()
	}
	return nil
}

func (l *LoggerT) Trace() {
	// msg := getFunctionNameOfCaller() + "()"
	msg := getIndentedFunctionNameOfCaller()
	l.CreateLogEntry(msg, LogLevelTrace)
}

func (l *LoggerT) Debug(msg string) {
	l.CreateLogEntry(msg, LogLevelDebug)
}

func (l *LoggerT) Debugf(format string, a ...interface{}) {
	l.CreateLogEntry(fmt.Sprintf(format, a...), LogLevelDebug)
}

func (l *LoggerT) Info(msg string) {
	l.CreateLogEntry(msg, LogLevelInfo)
}

func (l *LoggerT) Infof(format string, a ...interface{}) {
	l.CreateLogEntry(fmt.Sprintf(format, a...), LogLevelInfo)
}

func (l *LoggerT) InfoPrint(msg string) {
	fmt.Println(msg)
	l.Info(msg)
}

func (l *LoggerT) InfoPrintf(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	l.InfoPrint(msg)
}

func (l *LoggerT) Error(msg string) {
	// A disabled logger logs nothing and fires no callback. (The *Print helpers
	// still print, because they print before calling Error.)
	if l.IsDisabled {
		return
	}
	l.CreateLogEntry(msg, LogLevelError)
	if l.FnCallbackOnError != nil {
		l.FnCallbackOnError()
	}
}

func (l *LoggerT) Errorf(format string, a ...interface{}) {
	l.Error(fmt.Sprintf(format, a...))
}

func (l *LoggerT) ErrorfContext(_format string, a ...interface{}) {
	l.Error(callsite.SprintfContext(_format, a...))
}

func (l *LoggerT) ErrorE(err error) {
	l.Error(err.Error())
}

func (l *LoggerT) ErrorPrint(msg string) {
	fmt.Println("ERROR: " + msg)
	l.Error(msg)
}

func (l *LoggerT) ErrorPrintf(format string, a ...interface{}) {
	msg := fmt.Sprintf(format, a...)
	l.ErrorPrint(msg)
}

func (l *LoggerT) CreateLogEntry(msg string, level LogLevel) {
	if level < l.Level || l.IsDisabled {
		return
	}

	// Logging through an unconfigured stub logger is easy to do by accident
	// (e.g. a mismatched LoggerId) and otherwise silently writes to stderr
	// forever. Surface it once, then stay quiet.
	if l.IsStub && l.IsStubWarnedOnce.CompareAndSwap(false, true) {
		fmt.Fprintf(os.Stderr, "quicklog: logging through unconfigured stub logger %q (writing to stderr); call ConfigureLogger with this LoggerId to set it up\n", l.LoggerId)
	}

	// TODO: Use the pattern below to implement an option allowing
	//       a timezone location to be specified (e.g., to log as UTC)
	// ----------------------------------------------------------------
	// location, _ := time.LoadLocation("America/New_York")
	// now := time.Now().In(location)
	// ----------------------------------------------------------------

	timestamp := time.Now().Format("2006-01-02T15:04:05.000-0700")
	if _, err := l.RollingFile.Write([]byte(fmt.Sprintf("%s | %-5s | %s\n", timestamp, level.String(), msg))); err != nil {
		// Make the failure visible somewhere (stderr) on the first occurrence,
		// then stay quiet so a persistently broken sink doesn't spam.
		if l.IsLogWriteFailedOnce.CompareAndSwap(false, true) {
			fmt.Fprintf(os.Stderr, "quicklog: log write failed (%v); suppressing further write-failure notices for this logger\n", err)
		}
	}
}

func newRollingFile(config ConfigT) io.Writer {
	if err := os.MkdirAll(config.Directory, 0755); err != nil {
		fmt.Printf("quicklog: can't create log directory at %q (%v); falling back to stderr\n", config.Directory, err)
		return os.Stderr
	}

	return &lumberjack.Logger{
		Filename:   path.Join(config.Directory, config.Filename),
		MaxBackups: config.MaxBackups, // files
		MaxSize:    config.MaxSize,    // megabytes
		MaxAge:     config.MaxAge,     // days
	}
}

// packageVersion reports quicklog's module version as recorded in the build
// info of the program importing it. It returns "(devel)" when no version is
// stamped (e.g. when building within the quicklog module itself, or with build
// info unavailable).
func packageVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "(devel)"
	}
	// When quicklog is imported as a dependency, its version is recorded in Deps.
	for _, dep := range bi.Deps {
		if dep.Path == modulePath {
			if dep.Replace != nil {
				return dep.Replace.Version
			}
			return dep.Version
		}
	}
	// When building within the quicklog module itself, it is the main module and
	// typically carries no released version.
	if bi.Main.Path == modulePath && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "(devel)"
}

func getIndentedFunctionNameOfCaller() string {

	// Start with skip = 2 to get the CALLER of the function that CALLS this function.
	const depthOfCaller = 2

	// maxTraceDepth caps how far up the stack we walk (and therefore how deep we
	// indent). Goroutines that were not spawned from runtime.main never have
	// runtime.main (or gin's Context.Next) on their stack, so without this cap the
	// loop would walk all the way to the top of the goroutine's stack and produce
	// nonsensically deep indentation.
	const maxTraceDepth = 30

	skip := depthOfCaller
	prefix := ""
	for skip-depthOfCaller < maxTraceDepth {
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
