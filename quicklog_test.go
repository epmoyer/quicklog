package quicklog

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// capture redirects the given std stream (&os.Stdout or &os.Stderr) to a pipe
// while fn runs, and returns everything written to it. Not safe for concurrent
// use, so tests using it must not run in parallel.
func capture(t *testing.T, stream **os.File, fn func()) string {
	t.Helper()
	orig := *stream
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	*stream = w
	fn()
	w.Close()
	*stream = orig

	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	return string(data)
}

func captureStderr(t *testing.T, fn func()) string { return capture(t, &os.Stderr, fn) }
func captureStdout(t *testing.T, fn func()) string { return capture(t, &os.Stdout, fn) }

// readLog returns the contents of dir/name, failing the test if it can't be read.
func readLog(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	return string(data)
}

// configure is a helper that registers a real logger writing into a fresh temp
// directory and arranges for it to be closed when the test ends.
func configure(t *testing.T, cfg ConfigT) (*LoggerT, string) {
	t.Helper()
	dir := t.TempDir()
	cfg.Directory = dir
	if cfg.Filename == "" {
		cfg.Filename = "app.log"
	}
	log := ConfigureLogger(cfg)
	t.Cleanup(func() { Close(cfg.LoggerId) })
	return log, dir
}

func TestLogLevelString(t *testing.T) {
	cases := map[LogLevel]string{
		LogLevelTrace: "TRACE",
		LogLevelDebug: "DEBUG",
		LogLevelInfo:  "INFO",
		LogLevelError: "ERROR",
		LogLevel(99):  "unknown",
	}
	for level, want := range cases {
		if got := level.String(); got != want {
			t.Errorf("LogLevel(%d).String() = %q, want %q", level, got, want)
		}
	}
}

func TestSetDefaults(t *testing.T) {
	c := ConfigT{}
	c.SetDefaults()
	if c.MaxBackups != defaultMaxBackups {
		t.Errorf("MaxBackups = %d, want %d", c.MaxBackups, defaultMaxBackups)
	}
	if c.MaxSize != defaultMaxSize {
		t.Errorf("MaxSize = %d, want %d", c.MaxSize, defaultMaxSize)
	}
	if c.MaxAge != 0 {
		t.Errorf("MaxAge = %d, want 0 (intentionally not defaulted)", c.MaxAge)
	}

	// Explicit values must not be overwritten.
	c2 := ConfigT{MaxBackups: 2, MaxSize: 7, MaxAge: 3}
	c2.SetDefaults()
	if c2.MaxBackups != 2 || c2.MaxSize != 7 || c2.MaxAge != 3 {
		t.Errorf("SetDefaults overwrote explicit values: %+v", c2)
	}
}

func TestGetLoggerEmptyIdPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("GetLogger(\"\") should panic")
		}
	}()
	GetLogger("")
}

func TestConfigureEmptyIdPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("ConfigureLogger with empty LoggerId should panic")
		}
	}()
	ConfigureLogger(ConfigT{})
}

func TestGetLoggerReturnsStubAndSameInstance(t *testing.T) {
	id := t.Name()
	t.Cleanup(func() { Close(id) })

	a := GetLogger(id)
	if !a.isStub {
		t.Error("unconfigured logger should be a stub")
	}
	if a.rollingFile != os.Stderr {
		t.Error("stub logger should write to stderr")
	}
	if b := GetLogger(id); a != b {
		t.Error("GetLogger should return the same instance for the same id")
	}
}

func TestStubUpgradedInPlace(t *testing.T) {
	id := t.Name()
	stub := GetLogger(id)
	if !stub.isStub {
		t.Fatal("expected a stub before configuration")
	}

	real, dir := configure(t, ConfigT{LoggerId: id, Level: LogLevelInfo})
	if stub != real {
		t.Error("ConfigureLogger should upgrade the existing stub in place (same pointer)")
	}
	if stub.isStub {
		t.Error("stub flag should be cleared after configuration")
	}

	// Logging through the original stub reference should reach the real file.
	stub.Info("via-original-reference")
	if content := readLog(t, dir, "app.log"); !strings.Contains(content, "via-original-reference") {
		t.Errorf("log via upgraded stub reference not found in file:\n%s", content)
	}
}

func TestDoubleConfigurePanics(t *testing.T) {
	id := t.Name()
	configure(t, ConfigT{LoggerId: id})

	defer func() {
		if recover() == nil {
			t.Error("configuring the same LoggerId twice should panic")
		}
	}()
	ConfigureLogger(ConfigT{LoggerId: id, Directory: t.TempDir(), Filename: "app.log"})
}

func TestConfigureWritesBanner(t *testing.T) {
	_, dir := configure(t, ConfigT{LoggerId: t.Name(), Level: LogLevelInfo})
	content := readLog(t, dir, "app.log")
	if !strings.Contains(content, "BEGIN") {
		t.Errorf("expected BEGIN banner:\n%s", content)
	}
	if !strings.Contains(content, "quicklog ") {
		t.Errorf("expected version line:\n%s", content)
	}
}

func TestLevelFiltering(t *testing.T) {
	log, dir := configure(t, ConfigT{LoggerId: t.Name(), Level: LogLevelInfo})
	log.Debug("debug-msg")
	log.Info("info-msg")
	log.Error("error-msg")

	content := readLog(t, dir, "app.log")
	if strings.Contains(content, "debug-msg") {
		t.Error("Debug should be filtered out below Info level")
	}
	if !strings.Contains(content, "info-msg") || !strings.Contains(content, "error-msg") {
		t.Errorf("Info/Error should be logged:\n%s", content)
	}
}

func TestFormattingHelpers(t *testing.T) {
	log, dir := configure(t, ConfigT{LoggerId: t.Name(), Level: LogLevelTrace})
	log.Debugf("d=%d", 1)
	log.Infof("i=%d", 2)
	log.Errorf("e=%d", 3)

	content := readLog(t, dir, "app.log")
	for _, want := range []string{"d=1", "i=2", "e=3"} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %q in:\n%s", want, content)
		}
	}
}

func TestErrorCallbackFiresWhenEnabled(t *testing.T) {
	count := 0
	log, _ := configure(t, ConfigT{
		LoggerId:          t.Name(),
		Level:             LogLevelInfo,
		FnCallbackOnError: func() { count++ },
	})
	log.Error("boom")
	log.ErrorE(errors.New("boom2"))
	log.Info("not-an-error")
	if count != 2 {
		t.Errorf("FnCallbackOnError called %d times, want 2", count)
	}
}

func TestDisabledLoggerSuppressesLogAndCallback(t *testing.T) {
	called := false
	log, dir := configure(t, ConfigT{
		LoggerId:          t.Name(),
		Level:             LogLevelInfo,
		IsDisabled:        true,
		FnCallbackOnError: func() { called = true },
	})
	log.Info("nope")
	log.Error("nope-error")

	if called {
		t.Error("disabled logger must not fire FnCallbackOnError")
	}
	// File should be absent or empty: nothing (not even the banner) was written.
	if data, err := os.ReadFile(filepath.Join(dir, "app.log")); err == nil && len(data) > 0 {
		t.Errorf("disabled logger wrote to file: %q", data)
	}
}

func TestDisabledLoggerStillPrints(t *testing.T) {
	log, dir := configure(t, ConfigT{LoggerId: t.Name(), Level: LogLevelInfo, IsDisabled: true})

	out := captureStdout(t, func() { log.InfoPrint("still-printed") })
	if !strings.Contains(out, "still-printed") {
		t.Errorf("InfoPrint should print even when disabled, got %q", out)
	}
	if data, err := os.ReadFile(filepath.Join(dir, "app.log")); err == nil && strings.Contains(string(data), "still-printed") {
		t.Error("disabled logger should not write the printed message to file")
	}
}

func TestInfoPrintLogsAndPrints(t *testing.T) {
	log, dir := configure(t, ConfigT{LoggerId: t.Name(), Level: LogLevelInfo})

	out := captureStdout(t, func() { log.InfoPrint("hello-print") })
	if !strings.Contains(out, "hello-print") {
		t.Errorf("InfoPrint should print to stdout, got %q", out)
	}
	if content := readLog(t, dir, "app.log"); !strings.Contains(content, "hello-print") {
		t.Errorf("InfoPrint should also log to file:\n%s", content)
	}
}

func TestTraceRecordsCallerName(t *testing.T) {
	log, dir := configure(t, ConfigT{LoggerId: t.Name(), Level: LogLevelTrace})
	log.Trace()

	content := readLog(t, dir, "app.log")
	if !strings.Contains(content, "TRACE") {
		t.Errorf("Trace entry should be at TRACE level:\n%s", content)
	}
	if !strings.Contains(content, "TestTraceRecordsCallerName") {
		t.Errorf("Trace should record the calling function name:\n%s", content)
	}
}

func TestWriteFailureSurfacedOnce(t *testing.T) {
	id := t.Name()
	t.Cleanup(func() { Close(id) })

	// Take a stub and point it at a writer that always fails, masquerading as a
	// configured logger.
	log := GetLogger(id)
	log.rollingFile = failingWriter{}
	log.isStub = false

	out := captureStderr(t, func() {
		log.Info("one")
		log.Info("two")
		log.Info("three")
	})
	if got := strings.Count(out, "log write failed"); got != 1 {
		t.Errorf("write-failure notice appeared %d times, want exactly 1:\n%s", got, out)
	}
	if !log.isLogWriteFailedOnce.Load() {
		t.Error("isLogWriteFailedOnce should be set after a failed write")
	}
}

func TestStubLoggerWarnsOnce(t *testing.T) {
	id := t.Name()
	t.Cleanup(func() { Close(id) })

	out := captureStderr(t, func() {
		// Stub is created here, so its stderr sink is the captured pipe.
		log := GetLogger(id)
		log.Info("first")
		log.Info("second")
	})

	if got := strings.Count(out, "unconfigured stub logger"); got != 1 {
		t.Errorf("stub warning appeared %d times, want exactly 1:\n%s", got, out)
	}
	if !strings.Contains(out, id) {
		t.Errorf("stub warning should name the LoggerId %q:\n%s", id, out)
	}
	// The stub still emits the actual log lines to stderr.
	if !strings.Contains(out, "first") || !strings.Contains(out, "second") {
		t.Errorf("stub should still log to stderr:\n%s", out)
	}
}

func TestNewRollingFileFallsBackToStderr(t *testing.T) {
	// Place a regular file, then try to use a path *beneath* it as a directory;
	// MkdirAll fails because a parent component is not a directory.
	tmp := t.TempDir()
	file := filepath.Join(tmp, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var w io.Writer
	// Swallow the diagnostic line printed to stdout on the failure path.
	captureStdout(t, func() {
		w = newRollingFile(ConfigT{Directory: filepath.Join(file, "sub"), Filename: "app.log"})
	})
	if w != os.Stderr {
		t.Errorf("expected fallback to stderr on mkdir failure, got %T", w)
	}
}

func TestCloseRemovesFromRegistry(t *testing.T) {
	id := t.Name()
	log, _ := configure(t, ConfigT{LoggerId: id, Level: LogLevelInfo})
	log.Info("x")

	if err := Close(id); err != nil {
		t.Fatalf("Close: %v", err)
	}
	again := GetLogger(id)
	t.Cleanup(func() { Close(id) })
	if !again.isStub {
		t.Error("after Close, GetLogger should return a fresh stub")
	}
	if again == log {
		t.Error("after Close, GetLogger should return a new instance")
	}
}

func TestCloseUnknownIdIsNoop(t *testing.T) {
	if err := Close("no-such-logger-id"); err != nil {
		t.Errorf("Close of unknown id should return nil, got %v", err)
	}
}

func TestCloseDoesNotCloseStderr(t *testing.T) {
	id := t.Name()
	GetLogger(id) // stub writing to os.Stderr
	if err := Close(id); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// If Close had closed os.Stderr, this write would error.
	if _, err := os.Stderr.Write([]byte("")); err != nil {
		t.Errorf("os.Stderr was closed by Close: %v", err)
	}
}

func TestCloseAllEmptiesRegistry(t *testing.T) {
	dir := t.TempDir()
	ConfigureLogger(ConfigT{LoggerId: t.Name() + "-1", Directory: dir, Filename: "a.log"})
	ConfigureLogger(ConfigT{LoggerId: t.Name() + "-2", Directory: dir, Filename: "b.log"})

	if err := CloseAll(); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
	loggersMu.Lock()
	n := len(loggers)
	loggersMu.Unlock()
	if n != 0 {
		t.Errorf("registry should be empty after CloseAll, has %d entries", n)
	}
}

func TestPackageVersionNonEmpty(t *testing.T) {
	if got := packageVersion(); got == "" {
		t.Error("packageVersion() returned an empty string")
	}
}

// TestConcurrentStubCreation checks that simultaneous GetLogger calls for a
// previously-unknown id all observe the same stub (no duplicate registration).
// Run with -race to exercise the registry mutex.
func TestConcurrentStubCreation(t *testing.T) {
	id := t.Name()
	t.Cleanup(func() { Close(id) })

	const n = 50
	got := make([]*LoggerT, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = GetLogger(id)
		}(i)
	}
	wg.Wait()

	for i := 1; i < n; i++ {
		if got[i] != got[0] {
			t.Fatalf("concurrent GetLogger returned different instances (%p vs %p)", got[0], got[i])
		}
	}
}

// TestConcurrentLogging exercises concurrent registry reads and concurrent
// writes through a single configured logger. Run with -race for full value.
func TestConcurrentLogging(t *testing.T) {
	id := t.Name()
	log, _ := configure(t, ConfigT{LoggerId: id, Level: LogLevelInfo})

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			GetLogger(id)
			log.Infof("goroutine %d", i)
		}(i)
	}
	wg.Wait()
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
