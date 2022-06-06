# wasm_exec reference

This package contains imports and state needed by wasm go compiles when
`GOOS=js` and `GOARCH=wasm`.

## Introduction

When `GOOS=js` and `GOARCH=wasm`, Go's compiler targets WebAssembly 1.0 Binary
format (%.wasm).

Ex.
```bash
$ GOOS=js GOARCH=wasm go build -o my.wasm .
```

The operating system is "js", but more specifically it is [wasm_exec.js][1].
This package runs the `%.wasm` just like `wasm_exec.js` would.

## Identifying wasm compiled by Go

If you have a `%.wasm` file compiled by Go (via [asm.go][2]), it has a custom
section named "go.buildid".

You can verify this with wasm-objdump, a part of [wabt][3]:
```
$ wasm-objdump --section=go.buildid -x my.wasm

example3.wasm:  file format wasm 0x1

Section Details:

Custom:
- name: "go.buildid"
```

## Module Exports

Until [wasmexport][4] is implemented, the [compiled][2] WebAssembly exports are
always the same:

* "mem" - (memory 265) 265 = data section plus 16MB
* "run" - (func (param $argc i32) (param $argv i32)) the entrypoint
* "resume" - (func) continues work after a timer delay
* "getsp" - (func (result i32)) returns the stack pointer

## Module Imports

Go's [compiles][3] all WebAssembly imports in the module "go", and only
functions are imported.

Except for the "debug" function, all function names are prefixed by their go
package. Here are the defaults:

* "debug" - unknown
* "runtime.*" - supports system-call like functionality `GOARCH=wasm`
* "syscall/js.*" - supports the JavaScript model `GOOS=js`

Users can define their own "go" module function imports by defining a func
without a body in their source and a `%_wasm.s` or `%_js.s` file that uses the
`CallImport` instruction.

For example, given `func logString(msg string)` and the below assembly:
```assembly
#include "textflag.h"

TEXT ·logString(SB), NOSPLIT, $0
CallImport
RET
```

If the package was `main`, the WebAssembly function name would be
"main.logString". If it was `util` and your `go.mod` module was
"github.com/user/me", the WebAssembly function name would be
"github.com/prep/user/me/util.logString"

Regardless of whether the function import was built-in to Go, or defined by an
end user, the function type is the same as described below.

## CallImport conventions

The assembly `CallImport` instruction doesn't compile signatures to WebAssembly
function types. Instead, all functions have a single parameter of the stack
pointer, and implementors need to read the actual parameters from memory.
Similarly, if there are results, the implementation must write those to memory.

Specifically, any function compiled with `CallImport` has the same function
type: `(func (param $sp i32))`. `$sp` is the base memory offset to read and
write parameters to the stack (at 8 byte strides even if the value is 32-bit).

For example, `func walltime() (sec int64, nsec int32)` writes its results to
memory at offsets `sp+8` and `sp+16` respectively.

[1]: https://github.com/golang/go/blob/master/misc/wasm/wasm_exec.js
[2]: https://github.com/golang/go/blob/master/src/cmd/link/internal/wasm/asm.go
[3]: https://github.com/WebAssembly/wabt
[4]: https://github.com/golang/proposal/blob/master/design/42372-wasmexport.md
