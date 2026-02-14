package ptybackend

import (
	"sync"
	"testing"
	"time"
)

func TestEmbeddedStream_PublishAfterCloseDoesNotPanic(t *testing.T) {
	stream := &embeddedStream{
		events: make(chan OutputEvent, 1),
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}
	if ok := stream.publish(OutputEvent{Kind: OutputEventKindOutput, Data: []byte("x"), Seq: 1}); ok {
		t.Fatal("publish should fail after stream close")
	}
}

func TestEmbeddedStream_PublishDuringConcurrentCloseDoesNotPanic(t *testing.T) {
	stream := &embeddedStream{
		events: make(chan OutputEvent, 1),
	}

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = stream.publish(OutputEvent{Kind: OutputEventKindOutput, Data: []byte("x"), Seq: uint32(j)})
			}
		}()
	}

	time.Sleep(2 * time.Millisecond)
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for concurrent publishers to exit")
	}
}
