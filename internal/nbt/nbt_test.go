package nbt

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestSharedVectors(t *testing.T) {
	data, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors []struct {
		Name, Gzip, SHA256 string
		Error              bool
	}
	if err = json.Unmarshal(data, &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			compressed, err := base64.StdEncoding.DecodeString(v.Gzip)
			if err != nil {
				t.Fatal(err)
			}
			digest, err := Digest(compressed)
			if v.Error {
				if err == nil {
					t.Fatal("invalid NBT accepted")
				}
				return
			}
			if err != nil || hex.EncodeToString(digest[:]) != v.SHA256 {
				t.Fatalf("digest mismatch: %x, %v", digest, err)
			}
		})
	}
}

func FuzzDigest(f *testing.F) {
	data, _ := os.ReadFile("testdata/vectors.json")
	var vectors []struct{ Gzip string }
	json.Unmarshal(data, &vectors)
	for _, v := range vectors {
		raw, _ := base64.StdEncoding.DecodeString(v.Gzip)
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) { Digest(raw) })
}
