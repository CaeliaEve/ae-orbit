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

func TestNESQLItemIDMatchesSourceIdentityVectors(t *testing.T) {
	for _, vector := range []struct {
		registry string
		want     string
	}{
		{"BiblioCraft:Armor Stand", "item_49eeed3f0c9fd63f329598282a1406c58accbe6729a6195ebef95e6c4dcd84c1"},
		{"ProjRed|Core:projectred.core.part", "item_cc00b969a3e38a4bd9e7c34a9a4ce0d01befa1703559b3ed4b06a95031801347"},
		{"兼容|测试:方块 \"A\" /β", "item_ef3cfffdb74e2147983247b80182807f3ed49de488c2837dcc8d9e22e5f40d12"},
		{"owner:part:variant", "item_03e51f074fd995a201649bab53fc67bc0e92857f6598cc2692bb981dd02b37b0"},
	} {
		got, err := NESQLItemID(vector.registry, 0, nil)
		if err != nil || got != vector.want {
			t.Fatalf("%s: id=%s err=%v", vector.registry, got, err)
		}
	}
}
