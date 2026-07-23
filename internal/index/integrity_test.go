package index

import (
	"math"
	"testing"
)

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
