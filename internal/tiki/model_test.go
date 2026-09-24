package tiki

import (
	"encoding/json"
	"testing"
)

func TestParseIDRejectsNonDecimalInput(t *testing.T) {
	for _, input := range []string{"", "0", "-1", "+1", " 1", "1.0", "1e2", "١", "9223372036854775808"} {
		if _, err := ParseID(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}

func FuzzID(f *testing.F) {
	for _, input := range []string{`"1"`, `"9223372036854775807"`, `"+1"`, `"0001"`, `"0"`, `null`, `123`, `"\u0031"`, "\xff"} {
		f.Add([]byte(input))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		var id ID
		if err := json.Unmarshal(input, &id); err != nil {
			return
		}
		if id <= 0 {
			t.Fatalf("accepted nonpositive ID: %s", input)
		}
		encoded, err := json.Marshal(id)
		if err != nil {
			t.Fatal(err)
		}
		var decoded ID
		if err := json.Unmarshal(encoded, &decoded); err != nil || decoded != id {
			t.Fatalf("round trip failed: %s, %v", encoded, err)
		}
	})
}
