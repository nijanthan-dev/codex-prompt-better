package textutil

import (
	"math/rand"
	"testing"
)

func TestNormalizeLineEndingsAndWhitespace(t *testing.T) {
	got, err := Normalize("  Goal:\r\nvalue  \r\n\r\n\r\nStop:\r\ndone  ")
	if err != nil {
		t.Fatal(err)
	}
	want := "Goal:\nvalue\n\nStop:\ndone"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestNormalizeIdempotentProperty(t *testing.T) {
	random := rand.New(rand.NewSource(1))
	alphabet := []byte("abc XYZ\n\r\t:-_")
	for trial := 0; trial < 500; trial++ {
		size := 1 + random.Intn(300)
		data := make([]byte, size)
		for i := range data {
			data[i] = alphabet[random.Intn(len(alphabet))]
		}
		first, err := Normalize(string(data))
		if err != nil {
			continue
		}
		second, err := Normalize(first)
		if err != nil {
			t.Fatal(err)
		}
		if first != second {
			t.Fatalf("not idempotent: %q != %q", first, second)
		}
	}
}

func TestNormalizeRejectsInvalidUTF8(t *testing.T) {
	if _, err := Normalize(string([]byte{0xff})); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}
