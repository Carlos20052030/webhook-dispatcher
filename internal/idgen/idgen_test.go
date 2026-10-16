package idgen

import (
	"regexp"
	"testing"
)

var uuidV7Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestUUIDv7_Shape(t *testing.T) {
	for i := 0; i < 100; i++ {
		id, err := UUIDv7()
		if err != nil {
			t.Fatalf("UUIDv7: %v", err)
		}
		if !uuidV7Pattern.MatchString(id) {
			t.Fatalf("bad UUIDv7: %s", id)
		}
	}
}

func TestUUIDv7_NoCollision(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id, err := UUIDv7()
		if err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("collision: %s", id)
		}
		seen[id] = true
	}
}
