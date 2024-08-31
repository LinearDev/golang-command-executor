package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

var pwd string
var stdin, stdout, stderr bytes.Buffer
var stdoutoldlen int = 0

type NodeType int

const (
	Command NodeType = iota
	Operator
)

type Node struct {
	Type  NodeType
	Value string
	Left  *Node
	Right *Node
}

func main() {
	reader := bufio.NewReader(os.Stdin)
	pwd, _ = os.UserHomeDir()
	os.Chdir(pwd)

	go func() {
		for {
			out := stdout.String()

			if len(out)-stdoutoldlen > 0 {
				transformed := out[stdoutoldlen:]
				stdoutoldlen = len(out)
				fmt.Print(transformed)
			}
		}
	}()

	for {
		fmt.Print("> ")

		// Считываем строку, введенную пользователем
		input, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("Error reading input:", err)
			continue
		}

		// Удаляем лишние пробелы и символы новой строки
		input = strings.TrimSpace(input)

		// Если введена команда "exit", выходим из программы
		if input == "exit" {
			fmt.Println("Exiting...")
			break
		}

		tree := parseUnixCommand(input)

		// Обработка команды
		out, err := executeUnixCommand(nil, tree)
		if err != nil {
			if out == nil {
				fmt.Println(err)
			}
		}
	}
}

func parseUnixCommand(input string) *Node {
	input = strings.TrimSpace(input)

	if strings.HasPrefix(input, "(") && strings.HasSuffix(input, ")") {
		// Удаляем скобки и рекурсивно парсим содержимое
		return parseUnixCommand(input[1 : len(input)-1])
	}

	operators := []string{"||", "&&", "|", ">", "<"}
	for _, op := range operators {
		if idx := findOperatorOutsideBrackets(input, op); idx != -1 {
			// Создаем узел с оператором
			return &Node{
				Type:  Operator,
				Value: op,
				Left:  parseUnixCommand(input[:idx]),
				Right: parseUnixCommand(input[idx+len(op):]),
			}
		}
	}

	// Если операторов нет, считаем, что это команда
	return &Node{
		Type:  Command,
		Value: input,
	}
}

func findOperatorOutsideBrackets(input, operator string) int {
	level := 0
	for i := 0; i < len(input); i++ {
		if input[i] == '(' {
			level++
		} else if input[i] == ')' {
			level--
		} else if level == 0 && strings.HasPrefix(input[i:], operator) {
			return i
		}
	}
	return -1
}

// Функция для выполнения команды с поддержкой операторов
func executeUnixCommand(input []byte, node *Node) ([]byte, error) {
	switch node.Value {
	case "&&":
		_, err := executeUnixCommand(nil, node.Left)
		if err != nil {
			return nil, err
		}

		return executeUnixCommand(nil, node.Right)
	case "||":
		_, err := executeUnixCommand(nil, node.Left)
		if err == nil {
			return nil, err
		}

		return executeUnixCommand(nil, node.Right)
	case ">":
		// Handle output redirection
		if node.Right != nil && node.Right.Type == Command {
			output, err := executeUnixCommand(nil, node.Left)
			if err != nil {
				return nil, err
			}

			fileName := node.Right.Value
			err = os.WriteFile(fileName, output, 0644)
			if err != nil {
				return nil, err
			}
		}
		return nil, nil
	case "<":
		// Handle input redirection
		if node.Right != nil && node.Right.Type == Command {
			fileName := node.Right.Value
			fileContent, err := os.ReadFile(fileName)
			if err != nil {
				return nil, err
			}

			return executeUnixCommand(fileContent, node.Left)
		}
	case "|":
		leftOutput, err := executeUnixCommand(nil, node.Left)
		if err != nil {
			return nil, err
		}

		return executeUnixCommand(leftOutput, node.Right)
	default:
		return unixCommandExecutor(string(input), node.Value)
	}

	return nil, nil
}

func unixCommandExecutor(input, command string) ([]byte, error) {
	executed := unixMiddleware(command)

	if executed {
		return nil, nil
	}

	if len(input) != 0 {
		_, err := stdin.WriteString(input)
		if err != nil {
			return nil, err
		}
	}

	var cmd *exec.Cmd
	commWithArgs := strings.Fields(command)
	cmd = exec.Command(commWithArgs[0], commWithArgs[1:]...)
	cmd.Dir = pwd
	cmd.Stderr = &stderr
	cmd.Stdin = &stdin
	cmd.Stdout = &stdout

	err := cmd.Run()
	if err == nil {
		return stdout.Bytes(), nil
	}

	return nil, err
}

func unixMiddleware(command string) bool {
	commandWithArgs := strings.Fields(command)

	switch commandWithArgs[0] {
	case "cd":
		var dir string = pwd

		if len(commandWithArgs) < 2 || commandWithArgs[1] == "~" {
			// If no argument is provided or it's "~", go to the home directory
			dir, _ = os.UserHomeDir()
		} else if commandWithArgs[1] == ".." {
			// If ".." is provided, move up one directory
			dir = ".."
		} else {
			// Otherwise, navigate to the specified directory
			dir = commandWithArgs[1]
		}

		// Attempt to change the directory
		if err := os.Chdir(dir); err != nil {
			fmt.Println("Error changing directory:", err)
			return true
		}

		// Update the current working directory
		newDir, err := os.Getwd()
		if err != nil {
			fmt.Println("Error getting current directory:", err)
			return true
		}
		pwd = newDir

		return true
	}

	return false
}
