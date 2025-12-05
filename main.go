// Package main is a double fork process for Tinkerbell action containers.
//
// Run any arbitrary container image with its accompanying envs, command, volumes, etc.
// Wait an arbitrary amount of time before running your specified container image.
// Immediately report back to the Tink server that the action has completed successfully.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
)

const (
	// phaseEnv is the name phase that should be run. This is used internally and should be not set by the user.
	phaseEnv = "PHASE"
	// imageEnv is the name of the image that should be run for the second fork. This is set by the user.
	imageEnv = "IMAGE"
	// hostnameEnv is the name of the container that is running this process. Docker will set this.
	hostnameEnv = "HOSTNAME"
	// waitTimeEnv is the amount of time to wait before running the user image. This is set by the user. Default is 10 seconds.
	waitTimeEnv = "WAIT_SECONDS"
	// phaseSecondFork is the value of phaseEnv that indicates that the second fork should be run.
	phaseSecondFork = "SECOND_FORK"
	// dockerClientErrorCode is the exit code that should be used when the Docker client was not created successfully.
	dockerClientErrorCode = 12
	// firstForkErrorCode is the exit code that should be used when the first fork was not run successfully.
	firstForkErrorCode = 1
	// secondForkErrorCode is the exit code that should be used when the second fork was not run successfully.
	secondForkErrorCode = 2
	// defaultWaitTime is the amount of time to wait before running the user image.
	defaultWaitTime = time.Duration(10) * time.Second
)

func main() {
	phase := os.Getenv(phaseEnv)
	image := os.Getenv(imageEnv)
	hostname := os.Getenv(hostnameEnv)
	waitTime := os.Getenv(waitTimeEnv)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("starting waitdaemon", "phase", phase, "image", image, "hostname", hostname, "waitTime", waitTime)

	cl, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		logger.Info("unable to create Docker client", "error", err)
		os.Exit(dockerClientErrorCode)
	}

	if hn, err := os.Hostname(); err == nil {
		hostname = hn
	}
	statusCode := 0
	switch phase {
	case phaseSecondFork:
		logger.Info("running second fork")
		if err := secondFork(logger, cl, waitTime, image, hostname); err != nil {
			logger.Info("unable to run second fork image", "error", err)
			statusCode = secondForkErrorCode
		}
	default:
		logger.Info("running first fork")
		if err := firstFork(cl, hostname); err != nil {
			logger.Info("unable to run first fork image", "error", err)
			statusCode = firstForkErrorCode
		}
	}

	_ = cl.Close()
	os.Exit(statusCode)
}

// firstFork starts a container in the background from the image that is currently
// being used by the container. This must return immediately.
func firstFork(cl *client.Client, hostname string) error {
	con, err := cl.ContainerInspect(context.Background(), hostname)
	if err != nil {
		return err
	}
	con.Config.Env = append(con.Config.Env, fmt.Sprintf("%v=%v", phaseEnv, phaseSecondFork))

	return runContainer(cl, con)
}

func secondFork(logger *slog.Logger, cl *client.Client, waitTime string, image string, hostname string) error {
	logger.Info("pulling image", "image", image)
	if err := pullImage(cl, image); err != nil {
		logger.Info("unable to pull image", "error", err)
		return err
	}
	t := defaultWaitTime
	if s := waitTime; s != "" {
		if i, err := strconv.Atoi(s); err == nil {
			t = time.Duration(i) * time.Second
		}
	}
	logger.Info("waiting before running user image", "waitSeconds", t.String())
	time.Sleep(t)
	logger.Info("running user image", "image", image)
	if err := runUserImage(cl, image, hostname); err != nil {
		logger.Info("unable to run user defined image", "error", err)
		os.Exit(1)
	}
	return nil
}

func runContainer(cli *client.Client, self types.ContainerJSON) error {
	config := &container.Config{
		Image:        self.Config.Image,
		AttachStdout: self.Config.AttachStdout,
		AttachStderr: self.Config.AttachStderr,
		Cmd:          self.Config.Cmd,
		Tty:          self.Config.Tty,
		Env:          self.Config.Env,
	}

	hostConfig := &container.HostConfig{
		Privileged: self.HostConfig.Privileged,
		Binds:      self.HostConfig.Binds,
		PidMode:    self.HostConfig.PidMode,
	}

	c, err := cli.ContainerCreate(context.Background(), config, hostConfig, nil, nil, "")
	if err != nil {
		return err
	}

	return cli.ContainerStart(context.Background(), c.ID, container.StartOptions{})
}

func runUserImage(cli *client.Client, image string, hostname string) error {
	con, err := cli.ContainerInspect(context.Background(), hostname)
	if err != nil {
		return err
	}
	con.Config.Image = image
	// remove the PATH env var from the User container so that we don't override the existing PATH
	for i, env := range con.Config.Env {
		if strings.HasPrefix(env, "PATH") {
			con.Config.Env = append(con.Config.Env[:i], con.Config.Env[i+1:]...)
			break
		}
	}

	return runContainer(cli, con)
}

func pullImage(cli *client.Client, imageRef string) error {
	// Check if image already exists locally
	if _, err := cli.ImageInspect(context.Background(), imageRef); err == nil {
		return nil
	}

	// Image doesn't exist locally, pull it
	out, err := cli.ImagePull(context.Background(), imageRef, image.PullOptions{})
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(os.Stdout, out); err != nil {
		return err
	}

	return nil
}
