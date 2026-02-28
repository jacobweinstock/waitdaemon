// Package main is a double fork process for Tinkerbell action containers.
//
// Run any arbitrary container image with its accompanying envs, command, volumes, etc.
// Wait an arbitrary amount of time before running your specified container image.
// Immediately report back to the Tink server that the action has completed successfully.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jacobweinstock/waitdaemon/runtime"
	"github.com/jacobweinstock/waitdaemon/runtime/docker"
	"github.com/jacobweinstock/waitdaemon/runtime/nerdctl"
)

const (
	// phaseEnv is the name phase that should be run. This is used internally and should be not set by the user.
	phaseEnv = "PHASE"
	// imageEnv is the name of the image that should be run for the second fork. This is set by the user.
	imageEnv = "IMAGE"
	// waitTimeEnv is the amount of time to wait before running the user image. This is set by the user. Default is 10 seconds.
	waitTimeEnv = "WAIT_SECONDS"
	// runtimeEnv is the container runtime to use. Valid values: "docker", "nerdctl", "auto". Default is "auto".
	runtimeEnv = "CONTAINER_RUNTIME"
	// nerdctlNamespaceEnv is the nerdctl namespace nerdctl should operate in. Default is "tinkerbell".
	nerdctlNamespaceEnv = "NERDCTL_NAMESPACE"
	// nerdctlHostEnv enables nsenter mode. When set to "true" or "1", all nerdctl
	// CLI calls are prefixed with nsenter to enter host namespaces (mount, UTS, IPC,
	// net, PID). This eliminates the need for volume mounts in the Tinkerbell template
	// when using nerdctl. The container must still use pid: host.
	// Has no effect when using the Docker SDK.
	nerdctlHostEnv = "NERDCTL_HOST"
	// defaultNerdctlNamespace is the default namespace for nerdctl.
	defaultNerdctlNamespace = "tinkerbell"
	// phaseSecondFork is the value of phaseEnv that indicates that the second fork should be run.
	phaseSecondFork = "SECOND_FORK"
	// runtimeClientErrorCode is the exit code that should be used when the runtime client was not created successfully.
	runtimeClientErrorCode = 12
	// firstForkErrorCode is the exit code that should be used when the first fork was not run successfully.
	firstForkErrorCode = 1
	// secondForkErrorCode is the exit code that should be used when the second fork was not run successfully.
	secondForkErrorCode = 2
	// defaultWaitTime is the amount of time to wait before running the user image.
	defaultWaitTime = time.Duration(10) * time.Second
)

func main() {
	nsenter := nsenterEnabled()

	phase := os.Getenv(phaseEnv)
	img := os.Getenv(imageEnv)
	waitTime := os.Getenv(waitTimeEnv)
	runtimePref := os.Getenv(runtimeEnv)
	nerdctlNS := os.Getenv(nerdctlNamespaceEnv)
	if nerdctlNS == "" {
		nerdctlNS = defaultNerdctlNamespace
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	logger.Info("starting waitdaemon", "phase", phase, "image", img, "waitTime", waitTime, "runtime", runtimePref, "nerdctlNamespace", nerdctlNS)

	rt, err := runtime.Detect(runtimePref, dockerRuntime, nerdctlRuntime, nerdctlNS, nsenter)
	if err != nil {
		logger.Info("unable to create container runtime client", "error", err)
		os.Exit(runtimeClientErrorCode)
	}

	statusCode := 0
	switch phase {
	case phaseSecondFork:
		logger.Info("running second fork")
		if err := secondFork(logger, rt, waitTime, img); err != nil {
			logger.Info("unable to run second fork image", "error", err)
			statusCode = secondForkErrorCode
		}
	default:
		logger.Info("running first fork")
		if err := firstFork(logger, rt, img); err != nil {
			logger.Info("unable to run first fork image", "error", err)
			statusCode = firstForkErrorCode
		}
	}

	_ = rt.Close()
	os.Exit(statusCode)
}

// dockerRuntime creates a Docker runtime client.
func dockerRuntime() (runtime.Runtime, error) {
	return docker.New()
}

// nerdctlRuntime creates a ctrctl CLI-wrapper runtime client.
func nerdctlRuntime(cli []string) (runtime.Runtime, error) {
	return nerdctl.New(cli)
}

// firstFork pulls the user image and starts a container in the background from the image
// that is currently being used by the container. This must return immediately after
// creating the second container. Image pull failures are propagated back to the caller.
func firstFork(logger *slog.Logger, rt runtime.Runtime, img string) error {
	ctx := context.Background()

	// Pull the user's image before creating the second container.
	// This ensures pull failures are reported back to Tink server.
	if exists := rt.ImageExists(ctx, img); !exists {
		logger.Info("pulling image", "image", img)
		if err := rt.PullImage(ctx, img); err != nil {
			return fmt.Errorf("pulling image %q: %w", img, err)
		}
	} else {
		logger.Info("image already exists locally", "image", img)
	}

	info, err := rt.InspectSelf(ctx)
	if err != nil {
		return err
	}
	info.Env = append(info.Env, fmt.Sprintf("%v=%v", phaseEnv, phaseSecondFork))

	return rt.RunContainer(ctx, info)
}

func secondFork(logger *slog.Logger, rt runtime.Runtime, waitTime string, img string) error {
	ctx := context.Background()

	// Image was already pulled in firstFork, so we just wait and run.
	t := defaultWaitTime
	if s := waitTime; s != "" {
		if i, err := strconv.Atoi(s); err == nil {
			t = time.Duration(i) * time.Second
		}
	}
	logger.Info("waiting before running user image", "waitSeconds", t.String())
	time.Sleep(t)

	logger.Info("running user image", "image", img)
	if err := runUserImage(ctx, rt, img); err != nil {
		logger.Info("unable to run user defined image", "error", err)
		return err
	}

	return nil
}

func runUserImage(ctx context.Context, rt runtime.Runtime, img string) error {
	info, err := rt.InspectSelf(ctx)
	if err != nil {
		return err
	}
	info.Image = img

	// Strip the waitdaemon binary from the command.
	// The inspected Cmd is [/waitdaemon, user-cmd...], but the user image
	// doesn't have waitdaemon, so we pass only the user's command.
	if len(info.Cmd) > 1 && info.Cmd[0] == os.Args[0] {
		info.Cmd = info.Cmd[1:]
	}

	// remove the PATH env var from the User container so that we don't override the existing PATH
	info.Env = stripEnv(info.Env, "PATH")

	return rt.RunContainer(ctx, info)
}

// nsenterEnabled reports whether the NERDCTL_HOST env var is set to a truthy value.
// When the variable is unset (empty), it defaults to true.
func nsenterEnabled() bool {
	v := strings.ToLower(os.Getenv(nerdctlHostEnv))
	if v == "" {
		return true
	}
	return v == "true" || v == "1"
}

// stripEnv removes all environment variables with the given key prefix from the slice.
func stripEnv(envs []string, key string) []string {
	prefix := key + "="
	result := make([]string, 0, len(envs))
	for _, env := range envs {
		if !strings.HasPrefix(env, prefix) {
			result = append(result, env)
		}
	}
	return result
}
