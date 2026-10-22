package main

import "testing"

func TestEncode(t *testing.T) {
	if g := Encode(42.605, -5.603, 5); g != "ezs42" {
		t.Fatalf("got %s", g)
	}
	if g := Encode(1.2840, 103.8515, 6); g != "w21z71" {
		t.Fatalf("got %s", g)
	}
}

func TestCover(t *testing.T) {
	c := Cover(1.2840, 103.8515, 500, 6)
	if len(c) < 4 || len(c) > 9 {
		t.Fatalf("unexpected cover %v", c)
	}
	found := false
	for _, g := range c {
		if g == "w21z71" {
			found = true
		}
	}
	if !found {
		t.Fatalf("center cell missing: %v", c)
	}
}
