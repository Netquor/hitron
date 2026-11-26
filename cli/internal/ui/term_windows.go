//go:build windows

package ui

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

const (
	stdInputHandle       = ^uintptr(9) // -10 as uintptr
	enableProcessedInput = 0x0001
	enableLineInput      = 0x0002
	enableEchoInput      = 0x0004
	enableExtendedFlags  = 0x0080
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetStdHandle   = kernel32.NewProc("GetStdHandle")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
)

type rawState struct {
	handle uintptr
	mode   uint32
}

func consoleHandle() uintptr {
	handle, _, _ := procGetStdHandle.Call(stdInputHandle)
	return handle
}

func consoleMode(handle uintptr) (uint32, bool) {
	var mode uint32
	ok, _, _ := procGetConsoleMode.Call(handle, uintptr(unsafe.Pointer(&mode)))
	return mode, ok != 0
}

func setConsoleMode(handle uintptr, mode uint32) bool {
	ok, _, _ := procSetConsoleMode.Call(handle, uintptr(mode))
	return ok != 0
}

// IsTerminal reports whether stdin is a console.
func IsTerminal() bool {
	_, ok := consoleMode(consoleHandle())
	return ok
}

// EnterRaw disables line buffering and echo.
func EnterRaw() (*rawState, error) {
	handle := consoleHandle()
	mode, ok := consoleMode(handle)
	if !ok {
		return nil, fmt.Errorf("stdin is not a console")
	}
	raw := mode &^ (enableLineInput | enableEchoInput | enableProcessedInput)
	raw |= enableExtendedFlags
	if !setConsoleMode(handle, raw) {
		return nil, fmt.Errorf("could not set console mode")
	}
	return &rawState{handle: handle, mode: mode}, nil
}

func (r *rawState) Restore() {
	if r == nil || r.handle == 0 {
		return
	}
	setConsoleMode(r.handle, r.mode)
}

// ReadKey returns one byte from stdin.
func ReadKey() (byte, error) {
	var buffer [1]byte
	n, err := os.Stdin.Read(buffer[:])
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, fmt.Errorf("no input")
	}
	return buffer[0], nil
}

// ReadPassword reads a line with echo disabled.
func ReadPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stdout, prompt)
	handle := consoleHandle()
	mode, ok := consoleMode(handle)
	if !ok {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		return strings.TrimRight(line, "\r\n"), err
	}
	setConsoleMode(handle, mode&^enableEchoInput)
	defer func() {
		setConsoleMode(handle, mode)
		fmt.Fprintln(os.Stdout)
	}()
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

// Size returns columns and rows from the console environment.
func Size() (int, int) {
	width, height := 100, 32
	if value := os.Getenv("COLUMNS"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 20 {
			width = parsed
		}
	}
	if value := os.Getenv("LINES"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil && parsed > 8 {
			height = parsed
		}
	}
	return width, height
}
