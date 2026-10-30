//go:build linux

package hookstatus

import (
	"context"
	"golang.org/x/sys/unix"
)

func watchDirty(ctx context.Context, path string, dirty chan<- struct{}, ready chan<- struct{}) error {
	fd, err := unix.InotifyInit1(unix.IN_CLOEXEC | unix.IN_NONBLOCK)
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	if _, err = unix.InotifyAddWatch(fd, path, unix.IN_MODIFY|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF); err != nil {
		return err
	}
	wake := make([]int, 2)
	if err = unix.Pipe2(wake, unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
		return err
	}
	defer unix.Close(wake[0])
	defer unix.Close(wake[1])
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer close(finished); _, _ = unix.Write(wake[1], []byte{1}) })
	defer func() {
		if !stop() {
			<-finished
		}
	}()
	poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}, {Fd: int32(wake[0]), Events: unix.POLLIN}}
	buffer := make([]byte, 4096)
	for {
		_, err = unix.Poll(poll, -1)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if poll[1].Revents != 0 {
			return ctx.Err()
		}
		if poll[0].Revents != 0 {
			if _, err = unix.Read(fd, buffer); err != nil && err != unix.EAGAIN {
				return err
			}
			select {
			case dirty <- struct{}{}:
			default:
			}
		}
	}
}
