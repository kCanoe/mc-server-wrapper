package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
)

func validateStartup() error {
	// check that network connection is available
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Head("https://www.google.com")
	if err != nil || resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to confirm network connection: %w", err)
	}
	fmt.Println("confirmed network connection")
	// check that GH access token can be found
	if err := godotenv.Load(); err != nil {
		return fmt.Errorf("failed to load env file: %w", err)
	}
	if ghToken := os.Getenv("GH_PAT"); ghToken == "" {
		return fmt.Errorf("failed to load github access token")
	}
	fmt.Println("located github access token")
	// check JDK version and installation
	cmd := exec.Command("java", "-version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to find JDK installation: %w", err)
	}
	fmt.Printf("found JDK installation: %s\n", string(out))

	return nil
}

func fetchWorld() error {
	// passthrough for now
	return nil
}

func startServer() (*exec.Cmd, io.WriteCloser, error) {
	cmd := exec.Command("/opt/homebrew/opt/openjdk/bin/java", "-Xmx4G", "-jar", "server.jar", "nogui")
	serverJarPath := os.Getenv("SERVER_JAR_PATH")
	cmd.Stdout, cmd.Stderr, cmd.Dir = os.Stdout, os.Stderr, serverJarPath

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return cmd, nil, fmt.Errorf("failed to acquire pipe for server process: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return cmd, nil, fmt.Errorf("failed to start server instance: %w", err)
	}

	return cmd, stdin, nil
}

func shutdownServer(cmd *exec.Cmd, stdin io.WriteCloser) error {
	fmt.Println("sending stop command to server process")
	if _, err := io.WriteString(stdin, "stop\n"); err != nil {
		return fmt.Errorf("error writing to server process stdin: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("server process failed to close: %w", err)
	}
	return nil
}

func backupWorld() error {
	// passthrough for now
	return nil
}

func main() {
	// create listener for VM shutdown
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)

	// run server startup routine
	if err := validateStartup(); err != nil {
		log.Fatal(fmt.Errorf("failed to validate startup: %w", err))
	}
	if err := fetchWorld(); err != nil {
		log.Fatal(fmt.Errorf("failed to fetch remote world files: %w", err))
	}

	serverProcess, serverStdin, err := startServer()
	if err != nil {
		log.Fatal("failed to start server: %w", err)
	}

	// wait for VM shutdown signal
	sig := <-sigs
	fmt.Printf("recieved signal: %s, starting graceful shutdown\n", sig)

	// run server shutdown routine
	if err := shutdownServer(serverProcess, serverStdin); err != nil {
		log.Fatal("error shutting down server: %w", err)
	}
	if err := backupWorld(); err != nil {
		log.Fatal("error in backing up world files: %w", err)
	}

	fmt.Println("successful shutdown complete")
}
