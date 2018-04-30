package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/gliderlabs/ssh"
	"github.com/spf13/viper"
)

func dockerConnect(cfg *container.Config, sess ssh.Session, myinfo *AllocInfo) (*client.Client, types.Container, context.Context, error) {
	ctx := context.Background()
	myContainer := types.Container{}

	docker, err := client.NewClientWithOpts(client.WithHost(myinfo.DockerHost), client.WithVersion(viper.GetString("general.dockerapi")))
	if err != nil {
		log.Printf("Couldn't connect to %s", myinfo.DockerHost)
		return docker, myContainer, ctx, err
	}

	containers, err := docker.ContainerList(context.Background(), types.ContainerListOptions{})
	if err != nil {
		log.Println("Couldn't get a container listing")
		return docker, myContainer, ctx, err
	}

	for _, container := range containers {
		if container.Names[0] == "/"+myinfo.Name+"-"+myinfo.ID {
			log.Printf("found %s %s\n", container.Names[0], container.ID[0:10])
			myContainer = container
			break
		}
	}

	if myContainer.ID == "" {
		log.Printf("Coulnd't find container %s", "/"+myinfo.Name+"-"+myinfo.ID)
		err = fmt.Errorf("Couldn't find container")
		return docker, myContainer, ctx, err
	}

	fmt.Fprintf(sess, "welcome to %s on %s", myinfo.Name, myinfo.Node.Name)
	fmt.Fprintln(sess)

	return docker, myContainer, ctx, err
}

func dockerExec(cfg *container.Config, sess ssh.Session, myinfo *AllocInfo) (status int, err error) {
	status = 255

	docker, container, ctx, err := dockerConnect(cfg, sess, myinfo)
	if err != nil {
		return
	}

	execStartCheck := types.ExecStartCheck{
		Tty: true,
	}

	ec := types.ExecConfig{
		AttachStdout: cfg.AttachStdout,
		AttachStdin:  cfg.AttachStdin,
		AttachStderr: cfg.AttachStderr,
		Detach:       false,
		Tty:          true,
	}

	if myinfo.Command == "" {
		ec.Cmd = append(ec.Cmd, "/bin/sh")
	} else {
		ec.Cmd = append(ec.Cmd, myinfo.Command)
	}
	ec.User = "root"
	eresp, err := docker.ContainerExecCreate(context.Background(), container.ID, ec)
	if err != nil {
		log.Println("docker.ContainerExecCreate: ", err)
		return
	}

	stream, err := docker.ContainerExecAttach(ctx, eresp.ID, execStartCheck)
	if err != nil {
		log.Println("docker.ContainerExecAttach: ", err)
		return
	}
	defer stream.Close()

	outputErr := make(chan error)

	go func() {
		var err error
		if cfg.Tty {
			_, err = io.Copy(sess, stream.Reader)
		} else {
			_, err = stdcopy.StdCopy(sess, sess.Stderr(), stream.Reader)
		}
		outputErr <- err
	}()

	go func() {
		defer stream.CloseWrite()
		io.Copy(stream.Conn, sess)
	}()

	if cfg.Tty {
		_, winCh, _ := sess.Pty()
		go func() {
			for win := range winCh {
				err := docker.ContainerExecResize(ctx, eresp.ID, types.ResizeOptions{
					Height: uint(win.Height),
					Width:  uint(win.Width),
				})
				if err != nil {
					log.Println(err)
					break
				}
			}
		}()
	}
	for {
		inspect, err := docker.ContainerExecInspect(ctx, eresp.ID)
		if err != nil {
			log.Println(err)
		}
		if !inspect.Running {
			status = inspect.ExitCode
			break
		}
		time.Sleep(time.Second)
	}
	return
}

func dockerAttach(cfg *container.Config, sess ssh.Session, myinfo *AllocInfo) (status int, err error) {
	status = 255

	docker, container, ctx, err := dockerConnect(cfg, sess, myinfo)
	if err != nil {
		return
	}

	opts := types.ContainerAttachOptions{
		Stdin:  cfg.AttachStdin,
		Stdout: cfg.AttachStdout,
		Stderr: cfg.AttachStderr,
		Stream: true,
	}

	stream, err := docker.ContainerAttach(ctx, container.ID, opts)
	if err != nil {
		return
	}
	defer stream.Close()

	outputErr := make(chan error)
	stdinDone := make(chan struct{})
	go func() {
		var err error
		if cfg.Tty {
			_, err = io.Copy(sess, stream.Reader)
		} else {
			_, err = stdcopy.StdCopy(sess, sess.Stderr(), stream.Reader)
		}
		if err != nil {
			outputErr <- err
		}
		close(stdinDone)
	}()

	go func() {
		defer stream.CloseWrite()
		io.Copy(stream.Conn, sess)
	}()

	if cfg.Tty {
		_, winCh, _ := sess.Pty()
		go func() {
			for win := range winCh {
				err := docker.ContainerResize(ctx, container.ID, types.ResizeOptions{
					Height: uint(win.Height),
					Width:  uint(win.Width),
				})
				if err != nil {
					log.Println(err)
					break
				}
			}
		}()
	}
	resultC, errC := docker.ContainerWait(ctx, container.ID, "")
	select {
	case <-stdinDone:
		fmt.Println("STDIN closed")
		return
	case err = <-errC:
		fmt.Println("ERR", err)
		return
	case result := <-resultC:
		fmt.Println("RESULT:", result)
		status = int(result.StatusCode)
	}
	err = <-outputErr
	return
}

func dockerLogs(cfg *container.Config, sess ssh.Session, myinfo *AllocInfo, follow bool) (status int, err error) {
	status = 255

	docker, container, ctx, err := dockerConnect(cfg, sess, myinfo)
	if err != nil {
		return
	}

	stream, err := docker.ContainerLogs(ctx, container.ID, types.ContainerLogsOptions{ShowStdout: true, ShowStderr: true, Tail: "100", Follow: follow})
	if err != nil {
		log.Println("docker.ContainerLogs: ", err)
		return
	}
	defer stream.Close()

	if cfg.Tty {
		_, winCh, _ := sess.Pty()
		go func() {
			for win := range winCh {
				err := docker.ContainerResize(ctx, container.ID, types.ResizeOptions{
					Height: uint(win.Height),
					Width:  uint(win.Width),
				})
				if err != nil {
					log.Println(err)
					break
				}
			}
		}()
	}

	if cfg.Tty {
		_, err = io.Copy(sess, stream)
	} else {
		_, err = stdcopy.StdCopy(sess, sess.Stderr(), stream)
	}

	fmt.Println(err)
	return
}

func dockerInspect(cfg *container.Config, sess ssh.Session, myinfo *AllocInfo) (status int, err error) {
	status = 255

	docker, container, ctx, err := dockerConnect(cfg, sess, myinfo)
	if err != nil {
		return
	}
	_, res, err := docker.ContainerInspectWithRaw(ctx, container.ID, false)
	if err != nil {
		log.Println("docker.ContainerInspect: ", err)
		return
	}
	var i interface{}
	json.Unmarshal(res, &i)
	res, err = json.MarshalIndent(i, "", "   ")
	sess.Write(res)
	return
}
