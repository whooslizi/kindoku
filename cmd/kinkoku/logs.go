package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

const maxLog = 1 << 20

var logDir string

// startLogs sends fatal panics from any goroutine to <data>/logs/crash.log; Go writes nothing
// there unless the process crashes.
func startLogs(data string) {
	logDir = filepath.Join(data, "logs")
	if f := openLog("crash.log"); f != nil {
		debug.SetCrashOutput(f, debug.CrashOptions{})
		f.Close()
	}
}

// logError appends a failed command to <data>/logs/error.log.
func logError(err error) {
	if logDir == "" || err.Error() == "cancelled" {
		return
	}
	if f := openLog("error.log"); f != nil {
		fmt.Fprintf(f, "%s %s %s kinkoku %s: %v\n", time.Now().Format(time.RFC3339), version, arch,
			strings.Join(os.Args[1:], " "), err)
		f.Close()
	}
}

// openLog opens a log for appending, starting over once it passes maxLog (the old one is kept as .1).
func openLog(name string) *os.File {
	if os.MkdirAll(logDir, 0o755) != nil {
		return nil
	}
	path := filepath.Join(logDir, name)
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLog {
		os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return f
}
