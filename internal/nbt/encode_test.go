package nbt

import (
	"encoding/json"
	"testing"
)

func TestTypedNBTEncodingPreservesExactSourceIdentity(t *testing.T) {
	input := `{"type":"compound","value":{"name":{"type":"string","value":"custom"},"byte":{"type":"byte","value":"1"},"long":{"type":"long","value":"9223372036854775807"},"float":{"type":"float","value":"7fc00001"},"list":{"type":"list","element":"compound","value":[{"type":"compound","value":{"UID":{"type":"string","value":"bee"}}}]},"array":{"type":"int_array","value":["-1","2147483647"]}}}`
	raw, err := EncodeTypedJSON([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := TypedJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	var original any
	if err = json.Unmarshal([]byte(input), &original); err != nil {
		t.Fatal(err)
	}
	want, _ := canonicalDigest(original)
	got, _ := canonicalDigest(decoded)
	if want != got {
		t.Fatal("typed encoding changed", decoded)
	}
	other, err := EncodeTypedJSON([]byte(`{"type":"compound","value":{"byte":{"type":"int","value":"1"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	left, _ := Digest(raw)
	right, _ := Digest(other)
	if left == right {
		t.Fatal("NBT type collapsed")
	}
	for _, bad := range []string{`{"type":"byte","value":"1"}`, `{"type":"compound","value":{"bad":{"type":"byte","value":"128"}}}`, `{"type":"compound","value":{"list":{"type":"list","element":"int","value":[{"type":"byte","value":"1"}]}}}`} {
		if _, err = EncodeTypedJSON([]byte(bad)); err == nil {
			t.Fatal("invalid typed payload", bad)
		}
	}
}
