package netns

import (
	"errors"
	"fmt"
	"runtime"
	"sync"

	vnetns "github.com/vishvananda/netns"
)

// Opener runs fn inside a target netns; tests inject a fake that runs fn in place.
type Opener interface {
	DoInNetns(path string, fn func() error) error
}

var (
	hostNetnsOnce sync.Once
	hostNetns     vnetns.NsHandle
	hostNetnsErr  error
)

// InitHost must run on the main goroutine before any worker calls DoInNetns:
// it records the netns every DoInNetns call restores.
func InitHost() error {
	hostNetnsOnce.Do(func() {
		hostNetns, hostNetnsErr = vnetns.Get()
	})
	return hostNetnsErr
}

type realOpener struct{}

func NewOpener() Opener { return realOpener{} }

// ErrRestoreFailed means the OS thread is still in the target netns. The
// thread stays locked so the runtime retires it with the goroutine.
var ErrRestoreFailed = errors.New("netns restore failed; thread is poisoned")

func (realOpener) DoInNetns(path string, fn func() error) (retErr error) {
	if !hostNetns.IsOpen() {
		return errors.New("host netns not initialised; call InitHost() from main before spawning workers")
	}

	runtime.LockOSThread()
	unlock := true
	defer func() {
		if setErr := vnetns.Set(hostNetns); setErr != nil {
			restoreErr := fmt.Errorf("%w: %w", ErrRestoreFailed, setErr)
			if retErr == nil {
				retErr = restoreErr
			} else {
				retErr = fmt.Errorf("%w (also fn err: %w)", restoreErr, retErr)
			}
			unlock = false
		}
		if unlock {
			runtime.UnlockOSThread()
		}
	}()

	target, err := vnetns.GetFromPath(path)
	if err != nil {
		return fmt.Errorf("get netns from %s: %w", path, err)
	}
	defer target.Close()

	if err := vnetns.Set(target); err != nil {
		return fmt.Errorf("enter netns %s: %w", path, err)
	}
	return fn()
}
