package secret

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"
)

const dockerSecretsDir = "/run/secrets/"

// Load resolves a secret value using the following priority:
//  1. Docker secret file at /run/secrets/<name>
//  2. Environment variable <envKey>
//  3. Interactive terminal prompt (hidden input)
//
// Returns the resolved value. Exits the process if the value is required
// and cannot be obtained (e.g., non-interactive terminal with no env/secret).
func Load(name, envKey, promptLabel string) string {
	// 1. Docker secret (sanitize name to prevent path traversal)
	safeName := filepath.Base(name)
	if data, err := os.ReadFile(dockerSecretsDir + safeName); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			return s
		}
	}

	// 2. Environment variable
	if v := os.Getenv(envKey); v != "" {
		return v
	}

	// 3. Interactive prompt
	return promptHidden(promptLabel)
}

// LoadOptional is like Load but returns empty string instead of prompting
// when the value is not found in Docker secrets or env vars, unless
// shouldPrompt is true.
func LoadOptional(name, envKey, promptLabel string, shouldPrompt bool) string {
	// 1. Docker secret (sanitize name to prevent path traversal)
	safeName := filepath.Base(name)
	if data, err := os.ReadFile(dockerSecretsDir + safeName); err == nil {
		if s := strings.TrimSpace(string(data)); s != "" {
			return s
		}
	}

	// 2. Environment variable
	if v := os.Getenv(envKey); v != "" {
		return v
	}

	// 3. Prompt only if explicitly needed
	if shouldPrompt {
		return promptHidden(promptLabel)
	}
	return ""
}

// promptHidden reads a line from the terminal with masked feedback (* per character).
// Falls back to visible input if the terminal is not interactive (piped stdin).
func promptHidden(label string) string {
	fmt.Printf("  🔐 %s: ", label)

	// Check if stdin is a terminal
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		result, err := readMasked(fd)
		fmt.Println() // newline after masked input
		if err == nil {
			return strings.TrimSpace(result)
		}
		// Fallback to hidden input (no * feedback) if raw mode fails
		raw, err := term.ReadPassword(fd)
		fmt.Println()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(raw))
	}

	// Non-interactive fallback (piped input)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}
	return ""
}

// readMasked reads input one byte at a time in raw terminal mode,
// printing '*' for each character typed. Handles backspace and Enter.
func readMasked(fd int) (string, error) {
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(fd, oldState)

	var buf []byte
	b := make([]byte, 1)
	for {
		if _, err := os.Stdin.Read(b); err != nil {
			return string(buf), err
		}
		switch {
		case b[0] == '\r' || b[0] == '\n': // Enter
			return string(buf), nil
		case b[0] == 3 || b[0] == 4: // Ctrl+C / Ctrl+D
			return "", fmt.Errorf("input cancelled")
		case b[0] == 127 || b[0] == 8: // Backspace / DEL
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				fmt.Print("\b \b") // erase the last '*'
			}
		case b[0] >= 32 && b[0] <= 126: // printable ASCII
			buf = append(buf, b[0])
			fmt.Print("*")
		}
	}
}
