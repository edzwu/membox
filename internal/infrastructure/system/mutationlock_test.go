package system

import (
	"testing"
	"time"
)

func TestMutationLockSerializesIndependentOpeners(t *testing.T) {
	home := t.TempDir()
	first, err := OpenMutationLock(home)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := OpenMutationLock(home)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()

	if err := first.Lock(); err != nil {
		t.Fatal(err)
	}
	acquired := make(chan error, 1)
	releaseSecond := make(chan struct{})
	releasedSecond := make(chan error, 1)
	go func() {
		err := second.Lock()
		acquired <- err
		if err == nil {
			<-releaseSecond
			releasedSecond <- second.Unlock()
		}
	}()
	select {
	case err := <-acquired:
		t.Fatalf("second mutation entered before first released: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := first.Unlock(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-acquired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second mutation did not proceed after release")
	}
	close(releaseSecond)
	if err := <-releasedSecond; err != nil {
		t.Fatal(err)
	}
}

func TestMutationLockReentersOnSameGoroutine(t *testing.T) {
	lock, err := OpenMutationLock(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
}
