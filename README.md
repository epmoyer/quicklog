# quicklog

A small, dependency-light logging package for Go.

`quicklog` provides leveled logging to size-rolled files, a named-logger
registry so packages can share loggers without passing references around, and
convenience helpers that both log and print to the console.

## Features

- **Leveled logging** — `Trace`, `Debug`, `Info`, `Error`, with a configurable
  minimum level per logger.
- **Rolling files** — log files are rotated by size, count, and age via
  [lumberjack](https://github.com/natefinch/lumberjack).
- **Named logger registry** — retrieve a logger anywhere with
  `GetLogger(loggerId)`. Requesting an ID that has not been configured yet
  returns a *stub* logger that writes to `stderr`; once a real logger is
  registered under that ID, every holder of the stub is transparently upgraded
  to the real logger.
- **Log-and-print helpers** — `InfoPrint`, `ErrorPrint`, etc. write to the log
  and to the console in one call.
- **Trace indentation** — `Trace()` records the caller's function name,
  indented by call depth, for readable call-flow logs.
- **Error callback** — supply `FnCallbackOnError` to increment a counter or
  fire an alert whenever an error is logged.

## Installation

```sh
go get github.com/epmoyer/quicklog/v2
```

## Usage

```go
package main

import (
	"github.com/epmoyer/quicklog/v2"
)

func main() {
	log := quicklog.ConfigureLogger(quicklog.ConfigT{
		LoggerId:  "main",
		Directory: "logs",
		Filename:  "app.log",
		Level:     quicklog.LogLevelInfo,
	})

	log.Info("service started")
	log.Infof("listening on port %d", 8080)
	log.Error("something went wrong")
}
```

Retrieve the same logger from another package without passing it around:

```go
log := quicklog.GetLogger("main")
log.Debug("hello from another package")
```

If `"main"` has not been configured yet, `GetLogger` returns a stub logger that
writes to `stderr`. When `ConfigureLogger` is later called with the same
`LoggerId`, the stub is upgraded in place, so the reference above starts writing
to the real log file automatically.

## Configuration

`ConfigT` fields:

| Field               | Description                                                            | Default |
| ------------------- | ---------------------------------------------------------------------- | ------- |
| `LoggerId`          | Unique identifier for the logger instance (required).                  | —       |
| `Directory`         | Directory to write log files to.                                       | —       |
| `Filename`          | Log file name, placed inside `Directory`.                              | —       |
| `MaxSize`           | Max size in MB before a file is rolled.                                | `50`    |
| `MaxBackups`        | Max number of rolled files to keep.                                    | `5`     |
| `MaxAge`            | Max age in days to keep a rolled file (0 = no limit).                  | `0`     |
| `Level`             | Minimum `LogLevel` to log.                                             | `Trace` |
| `FnCallbackOnError` | Called whenever an error is logged (e.g. to increment an error count). | `nil`   |
| `IsDisabled`        | When true, suppress file logging; `*Print` helpers still print.        | `false` |

## License

[MIT](LICENSE.md)
