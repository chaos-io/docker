package docker

import (
	"context"
	"errors"
	"io"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/acarl005/stripansi"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
	"github.com/docker/go-units"

	"github.com/chaos-io/chaos/core"
	"github.com/chaos-io/chaos/logs"
)

const (
	OptionWorkingDir  = "workingDir"
	OptionEnv         = "env"
	OptionCpuSet      = "cpuset"
	OptionPorts       = "ports"
	OptionMemoryLimit = "memory"
	OptionAddHost     = "add-host"
	OptionAddDns      = "dns"
	OptionNetwork     = "network"
)

var (
	cli      *client.Client
	dictPath string
)

func init() {
	var err error
	cli, err = client.NewClientWithOpts(client.FromEnv, client.WithHost(Host), client.WithAPIVersionNegotiation())
	if err != nil {
		log.Panicf("failed to connect docker due to %v", err)
	}
}

// var Host = "http://172.17.0.1:2375"
var Host = "unix:///var/run/docker.sock"

// Run block to run the container, and waiting for stop
func Run(ctx context.Context, imageName, containerName string, cmd []string, options core.Options, bindPaths ...string) (int64, []byte, error) {
	logger := logs.With()
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithHost(Host), client.WithAPIVersionNegotiation())
	if err != nil {
		return 0, nil, err
	}
	defer func() {
		_ = c.Close()
	}()

	cmdBuilder := strings.Builder{}
	cmdBuilder.WriteString("docker run -it --rm")

	var env []string
	if envOption, ok := options[OptionEnv].([]string); ok {
		env = envOption
	}
	for _, ev := range env {
		cmdBuilder.WriteString(" --env '" + ev + "'")
	}

	cpuset := ""
	if cpusetOption, ok := options[OptionCpuSet].(string); ok {
		cpuset = cpusetOption
	}

	var ports []string
	if portsOption, ok := options[OptionPorts].([]string); ok {
		ports = portsOption
	}
	for _, p := range ports {
		cmdBuilder.WriteString(" -p " + p)
	}

	cfg := &container.Config{
		Image: imageName,
		Cmd:   cmd,
		Env:   env,
		Tty:   true,
	}

	var mounts []mount.Mount
	for i := 0; i < len(bindPaths)-1; i += 2 {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: bindPaths[i],
			Target: bindPaths[i+1],
		})

		cmdBuilder.WriteString(" -v " + bindPaths[i] + ":" + bindPaths[i+1])
	}

	hostConfig := &container.HostConfig{
		Mounts: mounts,
		Resources: container.Resources{
			CpusetCpus: cpuset,
		},
	}

	pset, pbindings, _ := nat.ParsePortSpecs(ports)
	if len(pbindings) > 0 {
		cfg.ExposedPorts = pset
		hostConfig.PortBindings = pbindings
	}

	resp, err := c.ContainerCreate(ctx, cfg, hostConfig, nil, nil, containerName)
	if err != nil {
		return 0, nil, err
	}

	cmdBuilder.WriteString(" " + imageName)
	for _, command := range cmd {
		cmdBuilder.WriteString(" " + command)
	}

	start := time.Now()
	logger.Infow("run docker container", "containerId", resp.ID, "cmd", cmdBuilder.String())

	if err = c.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{}); err != nil {
		return 0, nil, err
	}

	statusCh, errCh := c.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	logger.Debugw("container wait", "containerId", resp.ID, "duration", time.Since(start).String())

	var exitCode int64 = -1
	timeout := 0
	if timeoutOption, ok := options["timeout"].(int); ok {
		timeout = timeoutOption
	}
	if timeout > 0 {
		timeoutTimer := time.NewTimer(time.Duration(timeout) * time.Second)
		select {
		case err = <-errCh:
			timeoutTimer.Stop()
			if err != nil {
				return 0, nil, err
			}
		case status := <-statusCh:
			exitCode = status.StatusCode
			timeoutTimer.Stop()
		case <-timeoutTimer.C:
		}
	} else {
		select {
		case err = <-errCh:
			logger.Debugw("select error", "containerId", resp.ID, "error", err, "duration", time.Since(start).String())
			if err != nil {
				return 0, nil, err
			}
		case status := <-statusCh:
			logger.Debugw("select status", "containerId", resp.ID, "status", status, "duration", time.Since(start).String())
			exitCode = status.StatusCode
		}
	}

	out, err := containerLogs(c, resp.ID, types.ContainerLogsOptions{ShowStdout: true})
	if err != nil {
		logger.Warnw("failed to get the logs form container", "containerID", resp.ID, "error", err)
		return 0, nil, err
	}
	logger.Debugw("container logs", "containerId", resp.ID, "duration", time.Since(start).String())

	to := 2
	if err = c.ContainerStop(ctx, resp.ID, container.StopOptions{Timeout: &to}); err != nil {
		logger.Warn("failed to stop the container", "containerID", resp.ID, "error", err)
		return 0, out, nil
	}
	logger.Debugw("container stop", "containerId", resp.ID, "duration", time.Since(start).String())

	if err = c.ContainerRemove(ctx, resp.ID, types.ContainerRemoveOptions{Force: true}); err != nil {
		logger.Warn("failed to remove the container", "containerID", resp.ID, "error", err)
	}

	logger.Infow("run docker container successfully", "imageName", imageName, "containerId", resp.ID, "containerName", containerName, "exitCode", exitCode, "duration", time.Since(start).String())

	return exitCode, out, nil
}

func Start(ctx context.Context, imageName, containerName string, autoRemove bool, cmd []string, options core.Options, bindPaths ...string) (string, error) {
	logger := logs.With()
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithHost(Host), client.WithAPIVersionNegotiation())
	if err != nil {
		return "", err
	}
	defer func() {
		_ = c.Close()
	}()

	cmdBuilder := strings.Builder{}
	cmdBuilder.WriteString("docker run -it")
	if autoRemove {
		cmdBuilder.WriteString(" --rm ")
	}

	var env []string
	if envOption, ok := options[OptionEnv].([]string); ok {
		env = envOption
	}
	for _, ev := range env {
		cmdBuilder.WriteString(" --env '" + ev + "'")
	}

	cpuset := ""
	if cpusetOption, ok := options[OptionCpuSet].(string); ok {
		cpuset = cpusetOption
	}

	var ports []string
	if portsOption, ok := options[OptionPorts].([]string); ok {
		ports = portsOption
	}
	for _, p := range ports {
		cmdBuilder.WriteString(" -p " + p)
	}

	workingDir := ""
	if workingDirOption, ok := options[OptionWorkingDir].(string); ok {
		workingDir = workingDirOption
		cmdBuilder.WriteString(" -w " + workingDir)
	}

	cfg := &container.Config{
		Image:      imageName,
		Cmd:        cmd,
		Env:        env,
		Tty:        true,
		WorkingDir: workingDir,
	}

	addHost := []string{"host.docker.internal:host-gateway"}
	if addHostOption, ok := options[OptionAddHost].(string); ok && len(addHostOption) > 0 {
		hs := strings.Split(addHostOption, ",")
		for _, h := range hs {
			addHost = append(addHost, h)
			cmdBuilder.WriteString(" --add-host " + h)
		}
	}

	var _network string
	if nw, ok := options[OptionNetwork].(string); ok && len(nw) > 0 {
		_network = nw
		cmdBuilder.WriteString(" --network " + nw)
	}

	var mounts []mount.Mount
	for i := 0; i < len(bindPaths)-1; i += 2 {
		mounts = append(mounts, mount.Mount{
			Type:   mount.TypeBind,
			Source: bindPaths[i],
			Target: bindPaths[i+1],
		})
		cmdBuilder.WriteString(" -v " + bindPaths[i] + ":" + bindPaths[i+1])
	}

	hostConfig := &container.HostConfig{
		Mounts:      mounts,
		AutoRemove:  autoRemove,
		ExtraHosts:  addHost,
		NetworkMode: container.NetworkMode(_network),
		Resources: container.Resources{
			CpusetCpus: cpuset,
		},
	}

	if memoryLimit, ok := options[OptionMemoryLimit].(string); ok {
		if ml, err := units.FromHumanSize(memoryLimit); err == nil {
			hostConfig.Resources.Memory = ml
			hostConfig.Resources.MemorySwap = -1 // enable unlimited swap

			cmdBuilder.WriteString(" -m " + memoryLimit + " --memory-swap -1")
		}
	}

	pset, pbindings, _ := nat.ParsePortSpecs(ports)
	if len(pbindings) > 0 {
		cfg.ExposedPorts = pset
		hostConfig.PortBindings = pbindings
	}

	cmdBuilder.WriteString(" " + imageName)
	for _, command := range cmd {
		cmdBuilder.WriteString(" " + command)
	}

	logger.Infow("start the docker", "cmd", cmdBuilder.String())
	start := time.Now()
	resp, err := c.ContainerCreate(ctx, cfg, hostConfig, nil, nil, containerName)
	if err != nil {
		logger.Warnw("failed to create the container", "error", err, "config", cfg, "hostConfig", hostConfig, "containerName", containerName)
		return "", err
	}

	err = c.ContainerStart(ctx, resp.ID, types.ContainerStartOptions{})
	if err != nil {
		logger.Warnw("failed to start the container", "containerID", resp.ID, "error", err)
		return "", err
	}

	go Wait(ctx, resp.ID, func(exitCode int64, err error, duration time.Duration) {
		var containerLog string
		kvs := []interface{}{
			"containerId", resp.ID,
			"exitCode", exitCode,
			"duration", duration.String(),
			"imageName", imageName,
			"containerName", containerName,
			"cmd", cmdBuilder.String(),
		}

		if exitCode != 0 {
			buffer, err := Logs(resp.ID, 10)
			if err == nil {
				containerLog = string(buffer)
			}
			kvs = append(kvs, "containerLog", containerLog, "error", err)
		}

		logger.Infow("docker container stopped", kvs...)
	})

	logger.Infow("start the docker container successfully", "containerId", resp.ID, "duration", time.Since(start).String())

	return resp.ID, nil
}

// Wait waits until the specified container is not running
func Wait(ctx context.Context, id string, callback func(exitCode int64, err error, duration time.Duration)) {
	if callback == nil {
		return
	}
	start := time.Now()
	replyC, errC := cli.ContainerWait(ctx, id, container.WaitConditionNotRunning)

	var response container.WaitResponse
	var err error
	select {
	case response = <-replyC:
		if response.Error != nil {
			err = errors.New(response.Error.Message)
		}
	case err = <-errC:
	}

	callback(response.StatusCode, err, time.Since(start))
}

func Logs(id string, tail int) ([]byte, error) {
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithHost(Host), client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = c.Close()
	}()

	options := types.ContainerLogsOptions{ShowStdout: true}
	if tail > 0 {
		options.Tail = strconv.Itoa(tail)
	}

	return containerLogs(c, id, options)
}

func containerLogs(c *client.Client, id string, options types.ContainerLogsOptions) ([]byte, error) {
	out, err := c.ContainerLogs(context.Background(), id, options)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = out.Close()
	}()

	byt, err := io.ReadAll(out)
	if err != nil {
		return nil, err
	}
	return []byte(stripansi.Strip(string(byt))), nil
}

func Remove(id string) error {
	if len(id) == 0 {
		return nil
	}

	ctx := context.Background()
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithHost(Host), client.WithAPIVersionNegotiation())
	if err != nil {
		return err
	}
	defer func() {
		_ = c.Close()
	}()

	start := time.Now()
	if err = c.ContainerRemove(ctx, id, types.ContainerRemoveOptions{Force: true}); err != nil {
		logs.Warnw("failed to remove the container", "containerID", id, "error", err)
		return err
	}

	logs.Infow("remove the container successfully", "containerID", id, "duration", time.Since(start).String())

	return nil
}

func Commit(id string, image string) error {
	if len(id) == 0 || len(image) == 0 {
		return nil
	}

	ctx := context.Background()
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithHost(Host), client.WithAPIVersionNegotiation())
	if err != nil {
		return err
	}
	defer func() {
		_ = c.Close()
	}()

	_, err = c.ContainerCommit(ctx, id, types.ContainerCommitOptions{Reference: image})
	return err
}
