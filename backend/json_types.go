package backend

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// JSONSlice keeps empty Go slices in the array form expected by the frontend.
type JSONSlice[T any] []T

func (items JSONSlice[T]) MarshalJSON() ([]byte, error) {
	if items == nil {
		return []byte("[]"), nil
	}
	type plain JSONSlice[T]
	return json.Marshal(plain(items))
}

func (items *JSONSlice[T]) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("expected a JSON array")
	}
	type plain JSONSlice[T]
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*items = JSONSlice[T](decoded)
	return nil
}

// JSONBytes represents Rust Vec<u8> as a JSON array of numbers, not Go's
// default base64 string for []byte.
type JSONBytes []byte

func (value JSONBytes) MarshalJSON() ([]byte, error) {
	numbers := make([]int, len(value))
	for index, number := range value {
		numbers[index] = int(number)
	}
	return json.Marshal(numbers)
}

func (value *JSONBytes) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("expected a JSON array of byte values")
	}
	var numbers []int
	if err := json.Unmarshal(data, &numbers); err != nil {
		return err
	}
	decoded := make(JSONBytes, len(numbers))
	for index, number := range numbers {
		if number < 0 || number > 255 {
			return fmt.Errorf("byte value at index %d is out of range: %d", index, number)
		}
		decoded[index] = byte(number)
	}
	*value = decoded
	return nil
}
