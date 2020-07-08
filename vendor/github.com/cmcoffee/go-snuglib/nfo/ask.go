// Simple package to get user input from terminal.
package nfo

import (
	"bufio"
	"fmt"
	"golang.org/x/crypto/ssh/terminal"
	"os"
	"strings"
	"syscall"
)

var cancel = make(chan struct{})

// Function to restore terminal on event we get an interuption.
func getEscape() func() {
	s, _ := terminal.GetState(int(syscall.Stdin))
	return func() { terminal.Restore(0, s) }
}

// Loop until a non-blank answer is given
func NeedAnswer(prompt string, request func(prompt string) string) (output string) {
	for output = request(prompt); output == ""; output = request(prompt) {
	}
	return output
}

// Prompt to press enter.
func PressEnter(prompt string) {
	unesc := Defer(getEscape())
	defer unesc()

	fmt.Printf("\r%s", prompt)

	var blank_line []rune
	for _ = range prompt {
		blank_line = append(blank_line, ' ')
	}
	terminal.ReadPassword(1)
	fmt.Printf("\r%s\r", string(blank_line))
}

// Gets user input, used during setup and configuration.
func GetInput(prompt string) string {
	reader := bufio.NewReader(os.Stdin)

	fmt.Printf(prompt)
	response, _ := reader.ReadString('\n')
	response = cleanInput(response)

	return response
}

// Get Hidden/Password input, without returning information to the screen.
func GetSecret(prompt string) string {
	unesc := Defer(getEscape())
	defer unesc()

	fmt.Printf(prompt)
	resp, _ := terminal.ReadPassword(int(syscall.Stdin))
	output := cleanInput(string(resp))
	fmt.Printf("\n")
	return output
}

// Get confirmation
func GetConfirm(prompt string) bool {
	for {
		resp := GetInput(fmt.Sprintf("%s (y/n): ", prompt))
		resp = strings.ToLower(resp)
		if resp == "y" || resp == "yes" {
			return true
		} else if resp == "n" || resp == "no" {
			return false
		}
		continue
	}
}

// Removes newline characters
func cleanInput(input string) (output string) {
	var output_bytes []rune
	for _, v := range input {
		if v == '\n' || v == '\r' {
			continue
		}
		output_bytes = append(output_bytes, v)
	}
	return strings.TrimSpace(string(output_bytes))
}
