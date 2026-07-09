package storage

import (
	"io"
	"strings"
	"testing"
	"time"
)

type slowReader struct {
	release <-chan struct{}
	sent    bool
}

func (r *slowReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		copy(p, "slow")
		return 4, nil
	}
	<-r.release
	return 0, io.EOF
}

func TestPutEditedDoesNotBlockOtherDocumentMetadataReads(t *testing.T) {
	store, err := NewLocalStore(t.TempDir())
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	now := time.Now()
	for _, docID := range []string{"doc-a", "doc-b"} {
		if err := store.Put(docID, strings.NewReader("original"), Meta{
			DocumentID: docID,
			FileName:   docID + ".docx",
			CreatedAt:  now,
			ExpiresAt:  now.Add(time.Hour),
		}); err != nil {
			t.Fatalf("put %s: %v", docID, err)
		}
	}

	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_ = store.PutEdited("doc-a", &slowReader{release: release})
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	start := time.Now()
	readDone := make(chan error, 1)
	go func() {
		_, err := store.GetMeta("doc-b")
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatalf("get doc-b metadata: %v", err)
		}
	case <-time.After(100 * time.Millisecond):
		close(release)
		<-done
		t.Fatal("expected doc-b metadata read not to wait for doc-a write")
	}
	elapsed := time.Since(start)
	close(release)
	<-done

	if elapsed > 100*time.Millisecond {
		t.Fatalf("expected doc-b metadata read not to wait for doc-a write, took %s", elapsed)
	}
}
