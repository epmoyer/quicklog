# quicklog Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a
Changelog](http://keepachangelog.com/en/1.0.0/) and this project adheres
to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## Unreleased

## v2.1.0 2026-06-20
### Added
- `Close(loggerId)` and `CloseAll()` to flush/close logger files and remove them from the registry (e.g. on shutdown or before reconfiguring). The shared stderr/stdout streams used by stub loggers are never closed.

### Fixed
- Avoid a panic on the first write when the log directory cannot be created. `newRollingFile` now falls back to `stderr` instead of returning a nil writer.
- Guard the named-logger registry with a mutex, fixing a data race (and a racy double-configure check) when loggers are created or fetched concurrently.
- Cap stack walking in `Trace()` so calls from goroutines not descended from `runtime.main` no longer produce runaway indentation.
- Create the log directory with mode `0755` instead of `0744` so it is traversable.
- Make log-write failures observable: `CreateLogEntry` now checks the write error and reports the first failure for a logger to `stderr` (subsequent failures are suppressed to avoid spam).
- Make accidental use of an unconfigured stub logger observable: the first time a stub logger emits, it warns once on `stderr` (naming the `LoggerId`), then stays silent.

### Changed
- Derive the reported package version from build info (`debug.ReadBuildInfo`) instead of a hardcoded constant.
- A disabled logger (`IsDisabled`) no longer fires `FnCallbackOnError`; the callback now only runs when an error is actually logged. (The `*Print` helpers still print to the console.)
- Convert `LoggerT` methods to pointer receivers, matching how loggers are handed out (`*LoggerT`) and avoiding a per-call struct copy.
- Document that `ConfigT.MaxAge` has no default (0 disables age-based deletion; the rolled-file count is still bounded by `MaxBackups`), in both the code and the README.

## 2.0.0  2025-05-08
### Changed
- Adopt named logger registry pattern.
     - Now when you create a logger you must provide a loggerId to identify it.
     - Now you can call `GetLogger(loggerId)` to retrieve a logger by its ID.
     - Requesting a logger by Id which does not exist will return a stub logger that will print to sderr.
        - When/If a caller eventually DOES register a logger with that ID, the stub logger will be replaced with the real logger (so packages etc. that have a reference to the stub logger will automatically get the real logger when it is registered).
 
## 1.2.0  2025-02-26
### Changed
- `ConfigT.IsEnabled` -> `ConfigT.IsDisabled`
    - This makes logging default to "enabled" if one does not explicitly set the flag.

## 1.1.0  2025-02-24
### Added
- Merge `ConfigT.isEnabled` feature from `netdeviceagent` project.

## 1.0.0  2025-02-24
Baseline, from `nvweb` project.
