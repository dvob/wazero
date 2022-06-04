// Package wasm_exec contains imports needed by Go's misc/wasm/wasm_exec.js
// under the module name "go".
//
// Signature translation
//
// Except for "debug", functions in the "go" module all have a single uint32
// parameter `sp`. This is the base memory offset to read and write parameters
// to the stack (at 8 byte offsets). Ex If the corresponding Go signature has
// two parameters, they would be read from sp+8 and sp+16 respectively.
//
// See https://github.com/golang/go/blob/master/misc/wasm/wasm_exec.js
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/os_js.go
// See https://docs.google.com/document/d/131vjr4DH6JFnb-blm_uRdaC0_Nv3OUwjEY5qVCxCup4
package wasm_exec

import (
	"context"
	"fmt"
	"io"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	internalsys "github.com/tetratelabs/wazero/internal/sys"
	"github.com/tetratelabs/wazero/internal/wasm"
)

// Instantiate instantiates the "go" imports used by wasm_exec into the runtime
// default namespace.
//
// Notes
//
//	* Closing the wazero.Runtime has the same effect as closing the result.
//	* To instantiate into another wazero.Namespace, use NewBuilder instead.
func Instantiate(ctx context.Context, r wazero.Runtime) (api.Closer, error) {
	return NewBuilder(r).Instantiate(ctx, r)
}

// Builder configures the "go" imports used by wasm_exec.js for later use via
// Compile or Instantiate.
type Builder interface {

	// Compile compiles the "go" module that can instantiated in any namespace
	// (wazero.Namespace).
	//
	// Note: This has the same effect as this function on wazero.ModuleBuilder.
	Compile(context.Context, wazero.CompileConfig) (wazero.CompiledModule, error)

	// Instantiate instantiates the "go" module into the provided namespace.
	//
	// Note: This has the same effect as this function on wazero.ModuleBuilder.
	Instantiate(context.Context, wazero.Namespace) (api.Closer, error)
}

// NewBuilder returns a new Builder.
func NewBuilder(r wazero.Runtime) Builder {
	return &builder{r: r}
}

type builder struct {
	r wazero.Runtime
}

// moduleBuilder returns a new wazero.ModuleBuilder
func (b *builder) moduleBuilder() wazero.ModuleBuilder {
	g := &wasmExec{}
	return b.r.NewModuleBuilder("go").
		ExportFunction("runtime.wasmExit", g.wasmExit).
		ExportFunction("runtime.wasmWrite", g.wasmWrite).
		ExportFunction("runtime.resetMemoryDataView", g.resetMemoryDataView).
		ExportFunction("runtime.nanotime1", g.nanotime1).
		ExportFunction("runtime.walltime", g.walltime)
}

// Compile implements Builder.Compile
func (b *builder) Compile(ctx context.Context, config wazero.CompileConfig) (wazero.CompiledModule, error) {
	return b.moduleBuilder().Compile(ctx, config)
}

// Instantiate implements Builder.Instantiate
func (b *builder) Instantiate(ctx context.Context, ns wazero.Namespace) (api.Closer, error) {
	return b.moduleBuilder().Instantiate(ctx, ns)
}

// wasmExec holds defines the "go" imports used by wasm_exec.
type wasmExec struct {
}

// wasmWrite is used to implement runtime.exit.
//
// Here is this Wasm function name and its signature in runtime/sys_wasm.go:
//	* "runtime.wasmWrite" - `func wasmWrite(fd uintptr, p unsafe.Pointer, n int32)`
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/sys_wasm.go#L28
func (a *wasmExec) wasmExit(ctx context.Context, m api.Module, sp uint32) {
	code := requireReadUint32Le(ctx, m.Memory(), "code", sp+8)
	_ = m.CloseWithExitCode(ctx, code)
}

// wasmWrite is used to implement runtime.write which is used for panics and
// such, ex runtime.writeErr. It is only known to be used with fd = 2 (stderr).
//
// Here is this Wasm function name and its signature in runtime/os_js.go:
//	* "runtime.wasmWrite" - `func wasmWrite(fd uintptr, p unsafe.Pointer, n int32)`
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/os_js.go#L29
func (a *wasmExec) wasmWrite(ctx context.Context, m api.Module, sp uint32) {
	fd := uint32(requireReadUint64Le(ctx, m.Memory(), "fd", sp+8))
	p := requireReadUint64Le(ctx, m.Memory(), "p", sp+16)
	n := requireReadUint32Le(ctx, m.Memory(), "n", sp+24)

	var writer io.Writer

	switch fd {
	case 1:
		writer = getSysCtx(m).Stdout()
	case 2:
		writer = getSysCtx(m).Stderr()
	default:
		// Keep things simple by expecting nothing past 2
		panic(fmt.Errorf("unexpected fd %d", fd))
	}

	if _, err := writer.Write(requireRead(ctx, m.Memory(), "p", uint32(p), n)); err != nil {
		panic(fmt.Errorf("error writing p: %w", err))
	}
}

// resetMemoryDataView signals wasm.OpcodeMemoryGrow happened, indicating any
// cached view of memory should be reset.
//
// Here is this Wasm function name and its signature in runtime/mem_js.go:
//	* "runtime.resetMemoryDataView" - `func resetMemoryDataView()`
// See https://github.com/golang/go/blob/9839668b5619f45e293dd40339bf0ac614ea6bee/src/runtime/mem_js.go#L82
func (a *wasmExec) resetMemoryDataView(sp uint32) {
	// Nothing to do
}

// nanotime1 is used to implement runtime.nanotime.
//
// Here is this Wasm function name and its signature in runtime/sys_wasm.s:
//	* "runtime.nanotime1" - `func nanotime1() int64`
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/sys_wasm.s#L184
func (a *wasmExec) nanotime1(ctx context.Context, m api.Module, sp uint32) {
	nanos := getSysCtx(m).Nanotime(ctx)
	requireWriteUint64Le(ctx, m.Memory(), "t", sp+8, uint64(nanos))
}

// walltime is used to implement runtime.walltime.
//
// Here is this Wasm function name and its signature in runtime/sys_wasm.s:
//	* "runtime.walltime" - `func walltime() (sec int64, nsec int32)`
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/sys_wasm.s#L188
func (a *wasmExec) walltime(ctx context.Context, m api.Module, sp uint32) {
	sec, nsec := getSysCtx(m).Walltime(ctx)
	requireWriteUint64Le(ctx, m.Memory(), "sec", sp+8, uint64(sec))
	requireWriteUint32Le(ctx, m.Memory(), "nsec", sp+16, uint32(nsec))
}

func getSysCtx(m api.Module) *internalsys.Context {
	if internal, ok := m.(*wasm.CallContext); !ok {
		panic(fmt.Errorf("unsupported wasm.Module implementation: %v", m))
	} else {
		return internal.Sys
	}
}

// requireRead is like api.Memory except that it panics if the offset and
// byteCount are out of range.
func requireRead(ctx context.Context, mem api.Memory, fieldName string, offset, byteCount uint32) []byte {
	buf, ok := mem.Read(ctx, offset, byteCount)
	if !ok {
		panic(fmt.Errorf("out of range reading %s", fieldName))
	}
	return buf
}

// requireReadUint32Le is like api.Memory except that it panics if the offset
// is out of range.
func requireReadUint32Le(ctx context.Context, mem api.Memory, fieldName string, offset uint32) uint32 {
	result, ok := mem.ReadUint32Le(ctx, offset)
	if !ok {
		panic(fmt.Errorf("out of range reading %s", fieldName))
	}
	return result
}

// requireReadUint64Le is like api.Memory except that it panics if the offset
// is out of range.
func requireReadUint64Le(ctx context.Context, mem api.Memory, fieldName string, offset uint32) uint64 {
	result, ok := mem.ReadUint64Le(ctx, offset)
	if !ok {
		panic(fmt.Errorf("out of range reading %s", fieldName))
	}
	return result
}

// requireWriteUint64Le is like api.Memory except that it panics if the offset
// is out of range.
func requireWriteUint64Le(ctx context.Context, mem api.Memory, fieldName string, offset uint32, val uint64) {
	if ok := mem.WriteUint64Le(ctx, offset, val); !ok {
		panic(fmt.Errorf("out of range writing %s", fieldName))
	}
}

// requireWriteUint32Le is like api.Memory except that it panics if the offset
// is out of range.
func requireWriteUint32Le(ctx context.Context, mem api.Memory, fieldName string, offset uint32, val uint32) {
	if ok := mem.WriteUint32Le(ctx, offset, val); !ok {
		panic(fmt.Errorf("out of range writing %s", fieldName))
	}
}
