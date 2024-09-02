package golangcommandexecutor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"helpers"
)

var pwd string
var stdin, stdout, stderr bytes.Buffer
var stdoutoldlen int = 0
var commandExited bool = false
var commandAwaiting bool = false

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

type OutputTypes uint8

const (
	PWDChange       OutputTypes = 0
	Out             OutputTypes = 1
	WaitingForInput OutputTypes = 2
	Errored         OutputTypes = 3
	Exit            OutputTypes = 4
)

type Output struct {
	Type OutputTypes
	Data string
}

type APITerm struct {
	Command      string
	PWD          string
	PWDChan      chan string //pwd changes handler
	OutputChan   chan Output //channel for getting updates from `sdtout`
	StdInputChan chan string //if command requires output
}

func (api *APITerm) InitExecution() {
	pwd := api.PWD
	if len(api.PWD) == 0 {
		pwd, _ = os.UserHomeDir()

		//give new pwd string
		api.OutputChan <- Output{
			Type: PWDChange,
			Data: pwd,
		}
	}
	os.Chdir(pwd)

	stdout = bytes.Buffer{}
	stderr = bytes.Buffer{}

	go func() {
		for {
			out, err := helpers.UTF16BytesToString(stdout.Bytes())
			if err != nil {
				fmt.Println("errored to parse stdout to UTF16 string", err)
			}
			out = helpers.EscapeANSICodes(out)

			if len(out)-stdoutoldlen > 0 {
				transformed := strings.TrimSpace(out[stdoutoldlen:])
				stdoutoldlen = len(out)
				if commandAwaiting && len(transformed) != 0 {
					api.OutputChan <- Output{
						Type: Out,
						Data: transformed,
					}
				}
			}
		}

	}()

	input := strings.TrimSpace(api.Command)

	//build execution tree from input string
	tree := parseCommand(input)

	// run command node tree execution
	out, err := executeCommand(api, nil, tree)
	api.OutputChan <- Output{
		Type: Out,
		Data: helpers.EscapeANSICodes(string(out))[stdoutoldlen:],
	}
	//if execution is errored and command not returned error
	if err != nil && out == nil {
		//return error throw chanel
		api.OutputChan <- Output{
			Type: Errored,
			Data: err.Error(),
		}
	}

	api.OutputChan <- Output{
		Type: Exit,
		Data: "",
	}
}

// parses string and builds Command Node Tree
func parseCommand(input string) *Node {
	input = strings.TrimSpace(input)

	if strings.HasPrefix(input, "(") && strings.HasSuffix(input, ")") {
		// remove the brackets and recursively parse the contents
		return parseCommand(input[1 : len(input)-1])
	}

	// supported command operators
	operators := []string{"||", "&&", "|", ">", "<"}
	for _, op := range operators {
		if idx := findOperatorOutsideBrackets(input, op); idx != -1 {
			// build node with operator
			return &Node{
				Type:  Operator,
				Value: op,
				Left:  parseCommand(input[:idx]),
				Right: parseCommand(input[idx+len(op):]),
			}
		}
	}

	// If there are no operators, we consider it a terminal command
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

// function for command execution that supports operators
func executeCommand(api *APITerm, input []byte, node *Node) ([]byte, error) {
	switch node.Value {
	// if previous command execution result is true run next command
	case "&&":
		_, err := executeCommand(api, nil, node.Left)
		if err != nil {
			return nil, err
		}

		return executeCommand(api, nil, node.Right)
	// if previous command execution result is false run next command
	case "||":
		_, err := executeCommand(api, nil, node.Left)
		if err == nil {
			return nil, err
		}

		return executeCommand(api, nil, node.Right)
	// pipe from file output
	case ">":
		// Handle output redirection
		if node.Right != nil && node.Right.Type == Command {
			output, err := executeCommand(api, nil, node.Left)
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
	// pipe from file input
	case "<":
		// Handle input redirection
		if node.Right != nil && node.Right.Type == Command {
			fileName := node.Right.Value
			fileContent, err := os.ReadFile(fileName)
			if err != nil {
				return nil, err
			}

			return executeCommand(api, fileContent, node.Left)
		}
	//pipe from command to command
	//example `cat test | grep example.com`
	case "|":
		leftOutput, err := executeCommand(api, nil, node.Left)
		if err != nil {
			return nil, err
		}

		return executeCommand(api, leftOutput, node.Right)
	//as default run command
	default:
		return commandExecutor(api, string(input), node.Value)
	}

	return nil, nil
}

func commandExecutor(api *APITerm, input, command string) ([]byte, error) {
	//first run command throw middleware
	executed := middleware(command)
	//if middleware handle command as cd return output
	if executed {
		return nil, nil
	}

	//check if we need to pass input from prev command output
	if len(input) != 0 {
		_, err := stdin.WriteString(input)
		if err != nil {
			return nil, err
		}
	}

	var cmd *exec.Cmd
	done := make(chan bool)

	//create command from string -> [command, ...args]
	commWithArgs := strings.Fields(command)

	//create Cmd struct according to OS
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-Command", strings.Join(commWithArgs, " "))
	} else {
		cmd = exec.Command(commWithArgs[0], commWithArgs[1:]...)
	}

	cmd.Dir = pwd
	cmd.Stdin = bytes.NewReader([]byte(input))
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	// Handle input piping
	// if len(input) != 0 {
	// } else {
	// 	cmd.Stdin = os.Stdin
	// }

	//indicates if command expects input
	go func() {
		for {
			time.Sleep(500 * time.Millisecond)
			proc := cmd.Process
			if proc != nil && !commandExited {
				commandAwaiting = true
				api.OutputChan <- Output{
					Type: WaitingForInput,
					Data: "",
				}
			}

			if <-done {
				break
			}
		}
	}()

	//run command with waiting for result
	err := cmd.Run()
	done <- true
	if err != nil {
		return nil, err
	}

	return stdout.Bytes(), nil
}

func middleware(command string) bool {
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
