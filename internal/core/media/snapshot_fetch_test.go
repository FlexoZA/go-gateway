package media

import (
	"testing"
	"time"
)

func TestSnapshotFetchAssemblesBytes(t *testing.T) {
	sf := NewSnapshotFetch()
	done := sf.Begin("s1")
	if !sf.IsFetch("s1") {
		t.Fatal("IsFetch should be true after Begin")
	}
	sf.Write("s1", []byte("hello "))
	sf.Write("s1", []byte("world"))
	sf.Finish("s1")

	select {
	case got := <-done:
		if string(got) != "hello world" {
			t.Fatalf("got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("no bytes delivered")
	}
	if sf.IsFetch("s1") {
		t.Fatal("fetch should be removed after Finish")
	}
}

func TestSnapshotFetchAbort(t *testing.T) {
	sf := NewSnapshotFetch()
	done := sf.Begin("s2")
	sf.Write("s2", []byte("partial"))
	sf.Abort("s2")
	select {
	case got, ok := <-done:
		if ok && len(got) != 0 {
			t.Fatalf("abort should deliver no bytes, got %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("abort should close the channel")
	}
}

func TestSnapshotFetchOverflow(t *testing.T) {
	sf := NewSnapshotFetch()
	done := sf.Begin("s3")
	sf.Write("s3", make([]byte, MaxFetchBytes+1)) // over the cap → dropped
	sf.Finish("s3")
	select {
	case got := <-done:
		if got != nil {
			t.Fatalf("overflow should deliver nil, got %d bytes", len(got))
		}
	case <-time.After(time.Second):
		t.Fatal("no delivery")
	}
}

// Writes/Finish on an unknown or already-finished session must not panic.
func TestSnapshotFetchSafeNoSession(t *testing.T) {
	sf := NewSnapshotFetch()
	sf.Write("nope", []byte("x"))
	sf.Finish("nope")
	sf.Abort("nope")
	done := sf.Begin("s4")
	sf.Finish("s4")
	<-done
	sf.Finish("s4") // double finish: no panic
}
