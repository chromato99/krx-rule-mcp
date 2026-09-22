package index

import (
	"math"
	"testing"
)

func TestSnapshotArticleMatchesCanonicalHeadingIdentity(t *testing.T) {
	tests := []struct {
		name, article, heading string
		wantError              bool
	}{
		{"compact English", "§123-2", "§123-2. Foreign Securities", false},
		{"space before hyphen", "§123-2", "§123 -2. Foreign Securities", false},
		{"spaces around hyphen", "§123-2", "§123 - 2. Foreign Securities", false},
		{"different English section", "§123-2", "§123-20. Other Securities", true},
		{"different Korean article", "제1조", "제10조(다른 조문)", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := generationTestSnapshot(t)
			snapshot.Chunks[0].ArticleID = test.article
			snapshot.Chunks[0].HeadingPath = []string{test.heading}
			err := validateSnapshotStructure(snapshot)
			if (err != nil) != test.wantError {
				t.Fatalf("article %q heading %q: error=%v, wantError=%v", test.article, test.heading, err, test.wantError)
			}
		})
	}
}

func TestValidateVectorMapRejectsZeroFloat32Norm(t *testing.T) {
	tests := []struct {
		name   string
		vector []float64
	}{
		{name: "all zero", vector: []float64{0, 0}},
		{name: "underflow to zero", vector: []float64{math.SmallestNonzeroFloat64, 0}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateVectorMap(map[string][]float64{"chunk": test.vector}, []string{"chunk"}, 2, true); err == nil {
				t.Fatal("ValidateVectorMap accepted a zero-norm vector")
			}
		})
	}
	if err := ValidateVectorMap(map[string][]float64{"chunk": {1, 0}}, []string{"chunk"}, 2, true); err != nil {
		t.Fatalf("ValidateVectorMap rejected a valid vector: %v", err)
	}
}
