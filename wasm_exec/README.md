# wasm_exec

This package contains imports and state needed by wasm go compiles when
`GOOS=js` and `GOARCH=wasm`.

## Overview of Wasm compiled by Go

When `GOOS=js` and `GOARCH=wasm`, Go's compiler targets WebAssembly 1.0 Binary
format (%.wasm).

Ex.
```bash
$ GOOS=js GOARCH=wasm go build -o my.wasm .
```

The operating system is "js", but more specifically it is [wasm_exec.js][1].
This package runs the %.wasm just like wasm_exec.js would.

### Identifying Go compiled Wasm

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

### Module exports

Until [wasmexport][4] is implemented, the WebAssembly exports Go generates are
always the same:

* "mem" - (memory 0)
* "run" - (func (param $argc i32) (param $argv i32)) the entrypoint
* "resume" - (func) continues work after a timer delay
* "getsp" - (func (result i32)) returns the stack pointer

### Function Imports

Go's compiler generates WebAssembly imports in the module "go". Except for
the "debug" function, all function imports have a naming convention that's
prefixed by the package name. Here are the defaults:

* "debug" - unknown
* "runtime.*" - supports system-call like functionality
* "syscall/js.*" - supports the JavaScript model

All "go" imports have the same function type: `(func (param $sp i32))`. "sp" is
the base memory offset to read and write parameters to the stack (at 8 byte
strides even if the value is 32-bit).

For example, `func walltime() (sec int64, nsec int32)` writes its results to
memory at offsets `sp+8` and `sp+16` respectively.

Users can also define their own function imports in the "go" module directly.
To do so, they define a func without a body in their source. Then, they define
a `%_wasm.s` file that uses the `CallImport` instruction. The compiler (via
[asm.go][2]) chooses a naming prefix of "main.$funcName", if defined in the `main`
package, or a fully-qualified based on the module, if elsewhere.

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
end user, the function type is the same as described above.

[1]: https://github.com/golang/go/blob/master/misc/wasm/wasm_exec.js
[2]: https://github.com/golang/go/blob/master/src/cmd/link/internal/wasm/asm.go
[3]: https://github.com/WebAssembly/wabt
[4]: https://github.com/golang/proposal/blob/master/design/42372-wasmexport.md
