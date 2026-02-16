package manta

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/davecgh/go-spew/spew"
)

func init() {
	spew.Config.SortKeys = true
}

var debugLevel uint

func init() {
	if os.Getenv("DEBUG") != "" {
		debugLevel = 1
	}
	if os.Getenv("TRACE") != "" {
		debugLevel = 10
	}
}


// debugging level check
func v(level uint) bool {
	return level <= debugLevel
}

// printf only if debugging
func _debugf(format string, args ...interface{}) {
	if v(1) {
		args = append([]interface{}{_caller(2)}, args...)
		fmt.Printf("%s: "+format+"\n", args...)
	}
}

// error with printf syntax
func _errorf(format string, args ...interface{}) error {
	return fmt.Errorf(format, args...)
}

// panic with printf syntax
func _panicf(format string, args ...interface{}) {
	panic(fmt.Errorf(format, args...))
}

// Returns the name of the calling function
func _caller(n int) string {
	if pc, _, _, ok := runtime.Caller(n); ok {
		fns := strings.Split(runtime.FuncForPC(pc).Name(), "/")
		return fns[len(fns)-1]
	}

	return "unknown"
}
