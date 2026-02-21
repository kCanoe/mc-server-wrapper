package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"cloud.google.com/go/storage"
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

func startServer() (*exec.Cmd, io.WriteCloser, error) {
	cmd := exec.Command("java", "-Xmx4G", "-jar", "server.jar", "nogui")
	serverJarPath := os.Getenv("SERVER_JAR_PATH")
	cmd.Stdout, cmd.Stderr, cmd.Dir = os.Stdout, os.Stderr, serverJarPath

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

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

func uploadFile(bucket, object, file string) error {
	ctx := context.Background()
	client, err := storage.NewClient(ctx)
	if err != nil {
		return fmt.Errorf("storage.NewClient: %w", err)
	}
	defer client.Close()

	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("os.Open: %w", err)
	}
	defer f.Close()

	ctx, cancel := context.WithTimeout(ctx, time.Second*60)
	defer cancel()

	o := client.Bucket(bucket).Object(object)
	o = o.If(storage.Conditions{DoesNotExist: true})

	wc := o.NewWriter(ctx)
	if _, err = io.Copy(wc, f); err != nil {
		return fmt.Errorf("io.Copy: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("Writer.Close: %w", err)
	}
	fmt.Printf("blob %v uploaded.\n", object)
	return nil
}

func backupWorld() error {
	// compress the world files
	// create name - mc-world-[date]-[time].tar.gz
	timeString := time.Now().Format("2006-01-02T15:04:05")
	cleanTimeString := strings.ReplaceAll(timeString, ":", "-")
	nameString := "mc-world-backup" + "-" + cleanTimeString + ".tar.xz"

	fmt.Println("compressing world files")
	worldDir := os.Getenv("WORLD_NAME")
	compressCmd := exec.Command("tar", "-cJvf", nameString, worldDir)
	compressCmd.Dir = os.Getenv("SERVER_JAR_PATH")
	if err := compressCmd.Run(); err != nil {
		return fmt.Errorf("failed to compress world files: %w", err)
	}

	//(todo) upload the world files to gcs
	fmt.Println("uploading world files to storage bucket")
	filePath := path.Join(os.Getenv("SERVER_JAR_PATH"), nameString)
	uploadFile("world-archives", nameString, filePath)

	// clean up world files tar ball
	fmt.Println("cleaning up local archive file")
	deleteCmd := exec.Command("rm", nameString)
	deleteCmd.Dir = os.Getenv("SERVER_JAR_PATH")
	if err := deleteCmd.Run(); err != nil {
		return fmt.Errorf("failed to delete local world archive: %w", err)
	}

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
