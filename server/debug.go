package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// debugf writes a timestamped diagnostic line. By default it goes to stderr
// when LTM_DEBUG is set; when LTM_LOG_FILE is set it also (or only) goes to
// that file in append mode. Hosts vary in how they forward subprocess
// stderr — the file path is the reliable channel.
func debugf(format string, args ...any) {
	if !debugEnabled() {
		return
	}
	debugMu.Lock()
	defer debugMu.Unlock()
	w := debugWriter()
	if w == nil {
		return
	}
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	fmt.Fprintf(w, "[ltm %s] %s\n", ts, fmt.Sprintf(format, args...))
	// Best-effort flush for files; stderr is line-buffered on a pipe.
	if f, ok := w.(*os.File); ok {
		_ = f.Sync()
	}
}

var (
	debugMu     sync.Mutex
	debugCached *bool
	logFile     *os.File
	logFileOnce sync.Once
)

func debugEnabled() bool {
	if debugCached != nil {
		return *debugCached
	}
	v := os.Getenv("LTM_DEBUG")
	on := v == "1" || v == "true" || v == "yes" || v == "on"
	// LTM_LOG_FILE implies debug — convenient for hosts that drop stderr.
	if !on && os.Getenv("LTM_LOG_FILE") != "" {
		on = true
	}
	debugCached = &on
	return on
}

func debugWriter() io.Writer {
	logFileOnce.Do(func() {
		path := os.Getenv("LTM_LOG_FILE")
		if path == "" {
			return
		}
		f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ltm] cannot open LTM_LOG_FILE=%q: %v\n", path, err)
			return
		}
		logFile = f
	})
	if logFile != nil {
		return logFile
	}
	return os.Stderr
}
