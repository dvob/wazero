package main

import (
	"testing"

	"github.com/tetratelabs/wazero/internal/testing/maintester"
	"github.com/tetratelabs/wazero/internal/testing/require"
)

// Test_main ensures the following will work:
//
//	go run cat.go ./test.txt
func Test_rust(t *testing.T) {
	stdout, _ := maintester.TestMain(t, main, "cat", "rust/target/wasm32-wasi/release/cat.wasm", "./test.txt")
	require.Equal(t, "greet filesystem\n", stdout)
}

func Test_tinygo(t *testing.T) {
	stdout, _ := maintester.TestMain(t, main, "cat", "tinygo/cat.wasm", "./test.txt")
	require.Equal(t, "greet filesystem\n", stdout)
}
