package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Prompt asks for one line, with an optional default shown in brackets.
func Prompt(label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(os.Stdout, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(os.Stdout, "%s: ", label)
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return "", err
	}
	value := strings.TrimSpace(line)
	if value == "" {
		value = def
	}
	return value, nil
}

// Confirm asks a yes/no question. Anything other than y/yes is no.
func Confirm(question string) (bool, error) {
	fmt.Fprintf(os.Stdout, "%s [y/N]: ", question)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}
