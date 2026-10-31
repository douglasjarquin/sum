//go:build darwin

package hookstatus

import (
	"context"
	"golang.org/x/sys/unix"
	"os"
)

func watchDirty(ctx context.Context, path string, dirty chan<- struct{}, ready chan<- struct{}) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	fd, err := unix.Kqueue()
	if err != nil {
		return err
	}
	defer unix.Close(fd)
	changes := []unix.Kevent_t{{Ident: uint64(file.Fd()), Filter: unix.EVFILT_VNODE, Flags: unix.EV_ADD | unix.EV_CLEAR, Fflags: unix.NOTE_WRITE | unix.NOTE_DELETE | unix.NOTE_RENAME}, {Ident: 1, Filter: unix.EVFILT_USER, Flags: unix.EV_ADD | unix.EV_CLEAR}}
	zero := unix.Timespec{}
	if _, err = unix.Kevent(fd, changes, nil, &zero); err != nil {
		return err
	}
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(finished)
		_, _ = unix.Kevent(fd, []unix.Kevent_t{{Ident: 1, Filter: unix.EVFILT_USER, Fflags: unix.NOTE_TRIGGER}}, nil, &zero)
	})
	defer func() {
		if !stop() {
			<-finished
		}
	}()
	close(ready)
	events := make([]unix.Kevent_t, 1)
	for {
		_, err = unix.Kevent(fd, nil, events, nil)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if events[0].Filter == unix.EVFILT_USER {
			return ctx.Err()
		}
		select {
		case dirty <- struct{}{}:
		default:
		}
	}
}
