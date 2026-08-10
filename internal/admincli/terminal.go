package admincli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

var (
	// ErrTerminalRequired означает, что password попытались прочитать не из TTY.
	ErrTerminalRequired = errors.New("interactive terminal is required")
)

// TerminalPrompter читает login и скрытый password из одного терминала.
type TerminalPrompter struct {
	input  *os.File
	output io.Writer
	reader *bufio.Reader
}

// NewTerminalPrompter создаёт prompter только для интерактивного terminal.
func NewTerminalPrompter(input *os.File, output io.Writer) (*TerminalPrompter, error) {
	if input == nil || !term.IsTerminal(int(input.Fd())) {
		return nil, ErrTerminalRequired
	}

	return &TerminalPrompter{
		input:  input,
		output: output,
		reader: bufio.NewReader(input),
	}, nil
}

// ReadLine показывает prompt и читает одну обычную строку.
func (p *TerminalPrompter) ReadLine(prompt string) (string, error) {
	if _, err := fmt.Fprint(p.output, prompt); err != nil {
		return "", fmt.Errorf("write prompt: %w", err)
	}

	line, err := p.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read line: %w", err)
	}

	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

// ReadPassword показывает prompt и читает password без отображения символов.
func (p *TerminalPrompter) ReadPassword(prompt string) ([]byte, error) {
	if _, err := fmt.Fprint(p.output, prompt); err != nil {
		return nil, fmt.Errorf("write password prompt: %w", err)
	}

	value, err := term.ReadPassword(int(p.input.Fd()))
	if _, newlineErr := fmt.Fprintln(p.output); err == nil && newlineErr != nil {
		return nil, fmt.Errorf("write password prompt newline: %w", newlineErr)
	}
	if err != nil {
		return nil, fmt.Errorf("read hidden password: %w", err)
	}

	return value, nil
}
