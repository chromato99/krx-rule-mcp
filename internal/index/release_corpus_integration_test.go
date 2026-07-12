package index

import (
	"os"
	"strings"
	"testing"
)

func TestActualCorpusReleaseBuildsAndIndexesAttachmentFallbacks(t *testing.T) {
	if os.Getenv("KRX_DATA_TEST") != "1" {
		t.Skip("set KRX_DATA_TEST=1 to run collected-data release build")
	}
	snapshot, documents, err := BuildReleaseSnapshot(dataTestRoot())
	if err != nil {
		t.Fatalf("BuildReleaseSnapshot: %v", err)
	}
	if len(documents) < 80 || len(snapshot.Chunks) == 0 {
		t.Fatalf("release build is unexpectedly small: documents=%d chunks=%d", len(documents), len(snapshot.Chunks))
	}
	attachmentChunks := make(map[string]int)
	for _, chunk := range snapshot.Chunks {
		if chunk.Source == "attachment" {
			attachmentChunks[chunk.DocID]++
		}
	}
	fallbackDocuments := 0
	for _, document := range documents {
		if strings.TrimSpace(document.Body) != "" {
			continue
		}
		fallbackDocuments++
		if document.IsSearchable() || attachmentChunks[document.ID] == 0 {
			t.Fatalf("empty-body attachment fallback contract = %#v chunks=%d", document, attachmentChunks[document.ID])
		}
	}
	t.Logf("release documents=%d chunks=%d empty-body attachment fallbacks=%d", len(documents), len(snapshot.Chunks), fallbackDocuments)
}
