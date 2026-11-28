package artifacts

import (
	"bytes"
	"sync"
	"testing"
)

func TestPutConcurrentSameDigest(t *testing.T) {
	store := New(t.TempDir())
	data := bytes.Repeat([]byte("original"), 1<<16)
	digest := DigestOf(data)

	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			<-start
			got, size, err := store.Put(data)
			if err != nil || got != digest || size != int64(len(data)) {
				t.Errorf("Put = (%q, %d, %v), want (%q, %d, nil)", got, size, err, digest, len(data))
			}
		})
	}
	close(start)
	workers.Wait()
	got, err := store.Get(digest)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("wrong stored bytes: %v", err)
	}
	list, err := store.List()
	if err != nil || len(list) != 1 || list[0] != digest {
		t.Fatalf("List = %v, %v, want only %s", list, err, digest)
	}
}
