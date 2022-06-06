package main

import (
	"context"
	"embed"
	_ "embed"
	"io/fs"
	"log"
	"os"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/wasi_snapshot_preview1"
)

// catFS is an embedded filesystem limited to test.txt
//go:embed testdata/test.txt
var catFS embed.FS

// main writes an input file to stdout, just like `cat`.
//
// This is a basic introduction to the WebAssembly System Interface (WASI).
// See https://github.com/WebAssembly/WASI
func main() {
	if len(os.Args) < 2 {
		log.Fatalf("missing parameter: ./%s <module> <files>", os.Args[0])
	}

	moduleFilePath := os.Args[1]
	arguments := os.Args[2:]

	moduleBytes, err := os.ReadFile(moduleFilePath)
	if err != nil {
		log.Fatal(err)
	}

	// Choose the context to use for function calls.
	ctx := context.Background()

	// Create a new WebAssembly Runtime.
	r := wazero.NewRuntime()
	defer r.Close(ctx) // This closes everything this Runtime created.

	// Since wazero uses fs.FS, we can use standard libraries to do things like trim the leading path.
	rooted, err := fs.Sub(catFS, "testdata")
	if err != nil {
		log.Panicln(err)
	}

	// Combine the above into our baseline config, overriding defaults (which discards stdout and has no file system).
	config := wazero.NewModuleConfig().WithStdout(os.Stdout).WithStderr(os.Stderr).WithFS(rooted)

	// Instantiate WASI, which implements system I/O such as console output.
	if _, err = wasi_snapshot_preview1.Instantiate(ctx, r); err != nil {
		log.Panicln(err)
	}

	// Compile the WebAssembly module using the default configuration.
	code, err := r.CompileModule(ctx, moduleBytes, wazero.NewCompileConfig())
	if err != nil {
		log.Panicln(err)
	}

	wasiArguments := []string{"wasi"}
	wasiArguments = append(wasiArguments, arguments...)

	// InstantiateModule runs the "_start" function which is what TinyGo compiles "main" to.
	// * Set the program name (arg[0]) to "wasi" and add args to write "test.txt" to stdout twice.
	// * We use "/test.txt" or "./test.txt" because WithFS by default maps the workdir "." to "/".
	if _, err = r.InstantiateModule(ctx, code, config.WithArgs(wasiArguments...)); err != nil {
		log.Panicln(err)
	}
}
