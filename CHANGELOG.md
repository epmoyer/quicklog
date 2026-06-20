# quicklog Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a
Changelog](http://keepachangelog.com/en/1.0.0/) and this project adheres
to [Semantic Versioning](http://semver.org/spec/v2.0.0.html).

## Unreleased

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
