package cache

import (
	"testing"
	"time"
)

func TestCache_SetGet(t *testing.T) {
	c := New(1 * time.Minute)

	type testData struct {
		Name  string `json:"name"`
		Value int    `json:"value"`
	}

	// Set value
	c.Set("key1", testData{Name: "test", Value: 42})

	// Get value
	var result testData
	if !c.Get("key1", &result) {
		t.Error("Get should return true for existing key")
	}

	if result.Name != "test" || result.Value != 42 {
		t.Errorf("Got %+v, want {Name:test Value:42}", result)
	}
}

func TestCache_GetMissing(t *testing.T) {
	c := New(1 * time.Minute)

	var result string
	if c.Get("nonexistent", &result) {
		t.Error("Get should return false for missing key")
	}
}

func TestCache_Expiration(t *testing.T) {
	c := New(50 * time.Millisecond)

	c.Set("key", "value")

	var result string
	if !c.Get("key", &result) {
		t.Error("Get should return true before expiration")
	}

	// Wait for expiration
	time.Sleep(60 * time.Millisecond)

	if c.Get("key", &result) {
		t.Error("Get should return false after expiration")
	}
}

func TestCache_Overwrite(t *testing.T) {
	c := New(1 * time.Minute)

	c.Set("key", "first")
	c.Set("key", "second")

	var result string
	c.Get("key", &result)

	if result != "second" {
		t.Errorf("Got %s, want second", result)
	}
}

func TestCache_DifferentTypes(t *testing.T) {
	c := New(1 * time.Minute)

	// String
	c.Set("str", "hello")
	var str string
	if !c.Get("str", &str) || str != "hello" {
		t.Errorf("String: got %s, want hello", str)
	}

	// Int
	c.Set("int", 123)
	var num int
	if !c.Get("int", &num) || num != 123 {
		t.Errorf("Int: got %d, want 123", num)
	}

	// Slice
	c.Set("slice", []string{"a", "b", "c"})
	var slice []string
	if !c.Get("slice", &slice) || len(slice) != 3 {
		t.Errorf("Slice: got %v, want [a b c]", slice)
	}

	// Map
	c.Set("map", map[string]int{"x": 1, "y": 2})
	var m map[string]int
	if !c.Get("map", &m) || m["x"] != 1 {
		t.Errorf("Map: got %v, want map[x:1 y:2]", m)
	}
}

func TestCache_Concurrent(t *testing.T) {
	c := New(1 * time.Minute)

	done := make(chan bool)

	// Writer
	go func() {
		for i := 0; i < 100; i++ {
			c.Set("key", i)
		}
		done <- true
	}()

	// Reader
	go func() {
		for i := 0; i < 100; i++ {
			var result int
			c.Get("key", &result)
		}
		done <- true
	}()

	<-done
	<-done
}
