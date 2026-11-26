//go:build !windows

package ui

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// IsTerminal reports whether stdin is an interactive character device.
func IsTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// sttyOutput runs stty against our own stdin. Without cmd.Stdin set, the child
// reads /dev/null, so every query fails as "not a terminal" even in a real one.
func sttyOutput(args ...string) ([]byte, error) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	return cmd.Output()
}

type rawState struct {
	saved string
}

// stty runs stty against the process's own stdin. Without an explicit Stdin,
// os/exec gives the child /dev/null, so stty reports "not a terminal" and every
// TUI invocation fails before the first frame.
func stty(args ...string) *exec.Cmd {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	return cmd
}

// EnterRaw puts the terminal in raw mode without echo. Restore is safe to call
// more than once.
func EnterRaw() (*rawState, error) {
	out, err := sttyOutput("-g")
	if err != nil {
		return nil, fmt.Errorf("stdin is not a terminal")
	}
	saved := strings.TrimSpace(string(out))
	if err := stty("raw", "-echo").Run(); err != nil {
		return nil, err
	}
	return &rawState{saved: saved}, nil
}

func (r *rawState) Restore() {
	if r == nil || r.saved == "" {
		return
	}
	_ = stty(strings.Fields(r.saved)...).Run()
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
	out, err := sttyOutput("-g")
	if err != nil {
		line, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
		return strings.TrimRight(line, "\r\n"), readErr
	}
	saved := strings.TrimSpace(string(out))
	if err := stty("-echo").Run(); err != nil {
		return "", err
	}
	defer func() {
		_ = stty(strings.Fields(saved)...).Run()
		fmt.Fprintln(os.Stdout)
	}()
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimRight(line, "\r\n"), err
}

// Size returns columns and rows, preferring the values the shell exported.
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
	if width == 100 && height == 32 {
		if out, err := sttyOutput("size"); err == nil {
			fields := strings.Fields(string(out))
			if len(fields) == 2 {
				if rows, err := strconv.Atoi(fields[0]); err == nil && rows > 8 {
					height = rows
				}
				if cols, err := strconv.Atoi(fields[1]); err == nil && cols > 20 {
					width = cols
				}
			}
		}
	}
	return width, height
}
