# Golang Command Executor

This project is a command-line executor built in Go, which allows executing shell commands with support for logical operators (`&&`, `||`), pipes (`|`), and redirection (`<`, `>`). It includes an API for handling interactive commands, input/output redirection, and command execution across multiple platforms (Windows, Linux, macOS).

### Key Features:
 - __Command Parsing and Execution__: Supports chaining commands using logical operators (`&&`, `||`), and piping the output of one command into another (`|`).
 - __Redirection__: Handle file input/output redirection with `>` (output to file) and `<` (input from file).
 - __Cross-Platform__: Compatible with both Windows (PowerShell) and Unix-like systems (bash/sh).
 - __Middleware Support__: Custom middleware for handling commands such as `cd` directly within the program.
 - __Interactive API__: Provides channels for handling real-time command input/output, managing processes, and sending updates.
 - __ANSI Escape Code Handling__: Correctly processes and strips ANSI color codes from command output for clean display.

### Usage
You can create an instance of the APITerm struct to initialize a terminal session and execute commands. The terminal supports real-time output through channels, allowing interactive applications or terminal emulators.

### Example:
```go
api := &APITerm{
    Command:      "ls -la",
    PWD:          "/home/user",
    OutputChan:   make(chan Output),
    StdInputChan: make(chan string),
    CmdInterrupt: make(chan os.Signal),
}

go api.InitExecution()

for output := range api.OutputChan {
    fmt.Println(output.Data)
}
```

### Installation
```bash
go get github.com/your-username/golangcommandexecutor
```

### Contributing
Feel free to open issues or submit pull requests for improvements or bug fixes.