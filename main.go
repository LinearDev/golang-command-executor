package golangcommandexecutor

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"unicode"

	"helpers"
)

var sharedAPI *APITerm
var pwd string

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
	Type    OutputTypes
	Data    string
	Payload string
}

type APITerm struct {
	Command      string
	PWD          string
	OutputChan   chan Output //channel for getting updates from `sdtout`
	StdInputChan chan string //if command requires output
	CmdInterrupt chan os.Signal
}

func (api *APITerm) InitExecution() {
	sharedAPI = api
	pwd := api.PWD
	if len(api.PWD) == 0 {
		pwd, _ = os.UserHomeDir()
	}
	os.Chdir(pwd)
	//give new pwd string
	api.OutputChan <- Output{
		Type:    PWDChange,
		Data:    pwd,
		Payload: pwd,
	}

	done := make(chan bool)
	defer close(done)

	input := strings.TrimSpace(api.Command)

	//build execution tree from input string
	tree := parseCommand(input)

	// run command node tree execution
	out, err := executeCommand(api, nil, tree)

	//if execution is errored and command not returned error
	if err != nil && out == nil {
		//return error throw chanel
		api.OutputChan <- Output{
			Type: Errored,
			Data: err.Error(),
		}
		return
	}

	stringOut, _ := helpers.UTF16BytesToString(out)
	output := helpers.EscapeANSICodes(stringOut)

	if len(output) > 0 {
		api.OutputChan <- Output{
			Type: Out,
			Data: output,
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

	var cmd *exec.Cmd
	doneExec := make(chan bool)
	var stdout bytes.Buffer
	stdoutsended := 0
	commandAwaiting := false
	// commandExited = false
	StdinReader, StdinWriter := io.Pipe()

	//create Cmd struct according to OS
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-Command", command)
	} else {
		//create command from string -> [command, ...args]
		args, err := splitCommand(command)
		if err != nil {
			return []byte{}, err
		}
		cmd = exec.Command(args[0], args[1:]...)
	}

	cmd.Dir = pwd
	cmd.Stdout = &stdout
	cmd.Stdin = StdinReader

	go func() {
		defer StdinWriter.Close()

		//check if we need to pass input from prev command output
		if len(input) > 0 {
			_, err := StdinWriter.Write([]byte(input))
			if err != nil {
				log.Println("[ ERROR ] Unable to write to stdin:", err)
			}
		}

		// count := 0
		// outOnes := false

		for {
			select {
			case <-doneExec:
				commandAwaiting = false
				return
			default:
				if commandAwaiting {
					select {
					case msg := <-sharedAPI.StdInputChan:
						StdinWriter.Write([]byte(msg))
					default:
						outBytes := stdout.Bytes()
						out := helpers.EscapeANSICodes(string(outBytes[stdoutsended:]))
						stdoutsended = len(outBytes)
						if len(out) > 0 {
							// outOnes = true
							api.OutputChan <- Output{
								Type: Out,
								Data: out,
							}
						} else {
							// if !outOnes && count >= 25 {
							// 	time.Sleep(100 * time.Millisecond)
							// 	count += 1
							// } else {
							// 	return
							// }
						}
					}
				}
			}
		}
	}()

	//indicates if command expects input
	go func() {
		for {
			select {
			case <-doneExec:
				return
			case <-sharedAPI.CmdInterrupt:
				log.Println("[ INFO ] [ CommExec ] Received interrupt signal")
				commandAwaiting = false
				close(doneExec)
				cmd.Process.Kill()
				return
			default:
				time.Sleep(500 * time.Millisecond)
				proc := cmd.Process
				if proc == nil && !commandAwaiting {
					continue
				}

				state := cmd.ProcessState
				if !commandAwaiting && state == nil {
					commandAwaiting = true
				} else {
					if state != nil {
						if state.Exited() {
							commandAwaiting = false
							close(doneExec)
						}
					}
				}

			}
		}
	}()

	//run command with waiting for result
	err := cmd.Start()
	if err != nil {
		log.Println("[ ERROR ] Can not run command ", err)
		return nil, err
	}

	err = cmd.Wait()
	if err != nil {
		log.Println("[ ERROR ] Error awaiting command ", err)
		return nil, err
	}

	return stdout.Bytes()[stdoutsended:], nil
}

func middleware(command string) bool {
	commandWithArgs, _ := splitCommand(command)

	switch commandWithArgs[0] {
	case "cd":
		var dir = pwd

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

		sharedAPI.OutputChan <- Output{
			Type:    PWDChange,
			Data:    newDir,
			Payload: newDir,
		}

		return true
	}

	return false
}

func formPWDStruct() map[string]string {
	filesAndDirs := make(map[string]string)

	files, err := os.ReadDir(pwd)
	if err != nil {
		fmt.Println("Ошибка чтения каталога:", err)
		return filesAndDirs
	}

	for _, file := range files {
		fileType := "file"
		if file.IsDir() {
			fileType = "dir"
		}
		filesAndDirs[file.Name()] = fileType
	}

	return filesAndDirs
}

// splitCommand разбивает строку команды на аргументы
func splitCommand(command string) ([]string, error) {
	var args []string
	var currentArg strings.Builder
	inQuotes := false
	escape := false

	for _, r := range command {
		switch {
		case escape:
			// Если символ был экранирован, добавляем его в текущий аргумент
			currentArg.WriteRune(r)
			escape = false
		case r == '\\':
			// Если встретили символ экранирования, включаем флаг escape
			escape = true
		case r == '"' || r == '\'':
			// Обработка кавычек, если они начинаются или заканчиваются
			inQuotes = !inQuotes
		case unicode.IsSpace(r) && !inQuotes:
			// Если это пробел и мы не в кавычках, то аргумент завершен
			if currentArg.Len() > 0 {
				args = append(args, currentArg.String())
				currentArg.Reset()
			}
		default:
			// Иначе добавляем символ к текущему аргументу
			currentArg.WriteRune(r)
		}
	}

	// Добавляем последний аргумент, если есть
	if currentArg.Len() > 0 {
		args = append(args, currentArg.String())
	}

	if inQuotes {
		return nil, fmt.Errorf("mismatched quotes")
	}

	return args, nil
}
