package backend

import (
	"encoding/json"
	"testing"
)

func TestJSONSliceEncodesNilSliceAsEmptyArray(t *testing.T) {
	encoded, err := json.Marshal(JSONSlice[Context](nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "[]" {
		t.Fatalf("encoded nil slice = %s, want []", encoded)
	}

	var decoded JSONSlice[Context]
	if err := json.Unmarshal([]byte("[]"), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded == nil || len(decoded) != 0 {
		t.Fatalf("decoded empty array = %#v, want a non-nil empty slice", decoded)
	}
}

func TestJSONBytesRoundTripAsNumericArray(t *testing.T) {
	original := JSONBytes{0, 7, 128, 255}
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "[0,7,128,255]" {
		t.Fatalf("encoded bytes = %s, want numeric JSON array", encoded)
	}
	var decoded JSONBytes
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded) != string(original) {
		t.Fatalf("decoded bytes = %v, want %v", decoded, original)
	}
}

func TestJSONBytesRejectsOutOfRangeValues(t *testing.T) {
	for _, input := range []string{"[-1]", "[256]", "null"} {
		var decoded JSONBytes
		if err := json.Unmarshal([]byte(input), &decoded); err == nil {
			t.Errorf("expected %s to be rejected", input)
		}
	}
}
