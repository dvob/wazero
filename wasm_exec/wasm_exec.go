// Package wasm_exec contains imports and state needed by wasm go compiles when
// GOOS=js and GOARCH=wasm.
//
// See /wasm_exec/README.md for a deeper dive.
package wasm_exec

import (
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	internalsys "github.com/tetratelabs/wazero/internal/sys"
	"github.com/tetratelabs/wazero/internal/wasm"
	"github.com/tetratelabs/wazero/sys"
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
		ExportFunction("runtime.walltime", g.walltime).
		ExportFunction("runtime.scheduleTimeoutEvent", g.scheduleTimeoutEvent).
		ExportFunction("runtime.clearTimeoutEvent", g.clearTimeoutEvent).
		ExportFunction("runtime.getRandomData", g.getRandomData)
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
//
// Note: This is module-scoped, so only safe when used in a wazero.Namespace
// that only instantiates one module.
type wasmExec struct {
	mux                   sync.RWMutex
	nextCallbackTimeoutID uint32                 // guarded by mux
	scheduledTimeouts     map[uint32]*time.Timer // guarded by mux

	closed *uint64
}

// wasmExit implements runtime.wasmWrite which supports runtime.exit.
//
// Here is this Wasm function name and its signature in runtime/sys_wasm.go:
//	* "runtime.wasmExit" - `func wasmWrite(fd uintptr, p unsafe.Pointer, n int32)`
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/sys_wasm.go#L28
func (a *wasmExec) wasmExit(ctx context.Context, mod api.Module, sp uint32) {
	exitCode := requireReadUint32Le(ctx, mod.Memory(), "code", sp+8)

	closed := uint64(1) + uint64(exitCode)<<32 // Store exitCode as high-order bits.
	if !atomic.CompareAndSwapUint64(a.closed, 0, closed) {
		return
	}

	// TODO: free resources for this module
	_ = mod.CloseWithExitCode(ctx, exitCode)
}

// wasmWrite implements runtime.wasmWrite which supports runtime.write and
// runtime.writeErr. It is only known to be used with fd = 2 (stderr).
//
// Here is this Wasm function name and its signature in runtime/os_js.go:
//	* "runtime.wasmWrite" - `func wasmWrite(fd uintptr, p unsafe.Pointer, n int32)`
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/os_js.go#L29
func (a *wasmExec) wasmWrite(ctx context.Context, mod api.Module, sp uint32) {
	fd := uint32(requireReadUint64Le(ctx, mod.Memory(), "fd", sp+8))
	p := requireReadUint64Le(ctx, mod.Memory(), "p", sp+16)
	n := requireReadUint32Le(ctx, mod.Memory(), "n", sp+24)

	var writer io.Writer

	switch fd {
	case 1:
		writer = getSysCtx(mod).Stdout()
	case 2:
		writer = getSysCtx(mod).Stderr()
	default:
		// Keep things simple by expecting nothing past 2
		panic(fmt.Errorf("unexpected fd %d", fd))
	}

	if _, err := writer.Write(requireRead(ctx, mod.Memory(), "p", uint32(p), n)); err != nil {
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
	// TODO: Compiler-based memory.grow callbacks are ignored until we have a generic solution #601
}

// nanotime1 implements runtime.nanotime which supports time.Since.
//
// Here is this Wasm function name and its signature in runtime/sys_wasm.s:
//	* "runtime.nanotime1" - `func nanotime1() int64`
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/sys_wasm.s#L184
func (a *wasmExec) nanotime1(ctx context.Context, mod api.Module, sp uint32) {
	nanos := getSysCtx(mod).Nanotime(ctx)
	requireWriteUint64Le(ctx, mod.Memory(), "t", sp+8, uint64(nanos))
}

// walltime implements runtime.walltime which supports time.Now.
//
// Here is this Wasm function name and its signature in runtime/sys_wasm.s:
//	* "runtime.walltime" - `func walltime() (sec int64, nsec int32)`
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/sys_wasm.s#L188
func (a *wasmExec) walltime(ctx context.Context, mod api.Module, sp uint32) {
	sec, nsec := getSysCtx(mod).Walltime(ctx)
	requireWriteUint64Le(ctx, mod.Memory(), "sec", sp+8, uint64(sec))
	requireWriteUint32Le(ctx, mod.Memory(), "nsec", sp+16, uint32(nsec))
}

// scheduleTimeoutEvent implements runtime.scheduleTimeoutEvent which supports
// runtime.notetsleepg used by runtime.signal_recv.
//
// Unlike other most functions prefixed by "runtime.", this both launches a
// goroutine and invokes code compiled into wasm "resume".
//
// Here is this Wasm function name and its signature in runtime/sys_wasm.s:
//	* "runtime.scheduleTimeoutEvent" - `func scheduleTimeoutEvent(delay int64) int32`
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/sys_wasm.s#L192
func (a *wasmExec) scheduleTimeoutEvent(ctx context.Context, mod api.Module, sp uint32) {
	delayMs := requireReadUint64Le(ctx, mod.Memory(), "delay", sp+8)
	delay := time.Duration(delayMs) * time.Millisecond

	resume := mod.ExportedFunction("resume")

	// Invoke resume as an anonymous function, to propagate the context.
	callResume := func() {
		if err := a.failIfClosed(mod); err != nil {
			return
		}
		// While there's a possible error here, panicking won't help as it is
		// on a different goroutine.
		_, _ = resume.Call(ctx)
	}

	id := a.scheduleEvent(delay, callResume)
	requireWriteUint32Le(ctx, mod.Memory(), "id", sp+16, id)
}

// clearTimeoutEvent implements runtime.clearTimeoutEvent which supports
// runtime.notetsleepg used by runtime.signal_recv.
//
// Here is this Wasm function name and its signature in runtime/sys_wasm.s:
//	* "runtime.clearTimeoutEvent" - `func clearTimeoutEvent(id int32)`
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/sys_wasm.s#L196
func (a *wasmExec) clearTimeoutEvent(ctx context.Context, mod api.Module, sp uint32) {
	id := requireReadUint32Le(ctx, mod.Memory(), "id", sp+8)

	if t := a.removeEvent(id); t != nil {
		if !t.Stop() {
			<-t.C
		}
	}
}

// getRandomData implements runtime.getRandomData, which initializes the seed
// for runtime.fastrand.
//
// Here is this Wasm function name and its signature in runtime/sys_wasm.s:
//	* "runtime.getRandomData" - `func getRandomData(r []byte)`
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/sys_wasm.s#L200
func (a *wasmExec) getRandomData(ctx context.Context, mod api.Module, sp uint32) {
	buf := uint32(requireReadUint64Le(ctx, mod.Memory(), "buf", sp+8))
	bufLen := uint32(requireReadUint64Le(ctx, mod.Memory(), "bufLen", sp+16))

	randSource := getSysCtx(mod).RandSource()

	r := requireRead(ctx, mod.Memory(), "r", buf, bufLen)

	if n, err := randSource.Read(r); err != nil {
		panic(fmt.Errorf("RandSource.Read(r /* len =%d */) failed: %w", bufLen, err))
	} else if n != int(bufLen) {
		panic(fmt.Errorf("RandSource.Read(r /* len =%d */) read %d bytes", bufLen, n))
	}
}

// removeEvent removes an event previously scheduled with scheduleEvent or
// returns nil, if it was already removed.
func (a *wasmExec) removeEvent(id uint32) *time.Timer {
	a.mux.Lock()
	defer a.mux.Unlock()

	t, ok := a.scheduledTimeouts[id]
	if ok {
		delete(a.scheduledTimeouts, id)
		return t
	}
	return nil
}

// failIfClosed returns a sys.ExitError if wasmExit was called.
func (a *wasmExec) failIfClosed(mod api.Module) error {
	if closed := atomic.LoadUint64(a.closed); closed != 0 {
		return sys.NewExitError(mod.Name(), uint32(closed>>32)) // Unpack the high order bits as the exit code.
	}
	return nil
}

// getSysCtx returns the sys.Context from the module or panics.
func getSysCtx(mod api.Module) *internalsys.Context {
	if internal, ok := mod.(*wasm.CallContext); !ok {
		panic(fmt.Errorf("unsupported wasm.Module implementation: %v", mod))
	} else {
		return internal.Sys
	}
}

// scheduleEvent schedules an event onto another goroutine after d duration and
// returns a handle to remove it (removeEvent).
func (a *wasmExec) scheduleEvent(d time.Duration, f func()) uint32 {
	a.mux.Lock()
	defer a.mux.Unlock()

	id := a.nextCallbackTimeoutID
	a.nextCallbackTimeoutID++
	a.scheduledTimeouts[id] = time.AfterFunc(d, f)
	return id
}

// requireRead is like api.Memory except that it panics if the offset and
// byteCount are out of range.
func requireRead(ctx context.Context, mem api.Memory, fieldName string, offset, byteCount uint32) []byte {
	buf, ok := mem.Read(ctx, offset, byteCount)
	if !ok {
		panic(fmt.Errorf("Memory.Read(ctx, %d, %d) out of range of memory size %d reading %s",
			offset, byteCount, mem.Size(ctx), fieldName))
	}
	return buf
}

// requireReadUint32Le is like api.Memory except that it panics if the offset
// is out of range.
func requireReadUint32Le(ctx context.Context, mem api.Memory, fieldName string, offset uint32) uint32 {
	result, ok := mem.ReadUint32Le(ctx, offset)
	if !ok {
		panic(fmt.Errorf("Memory.ReadUint64Le(ctx, %d) out of range of memory size %d reading %s",
			offset, mem.Size(ctx), fieldName))
	}
	return result
}

// requireReadUint64Le is like api.Memory except that it panics if the offset
// is out of range.
func requireReadUint64Le(ctx context.Context, mem api.Memory, fieldName string, offset uint32) uint64 {
	result, ok := mem.ReadUint64Le(ctx, offset)
	if !ok {
		panic(fmt.Errorf("Memory.ReadUint64Le(ctx, %d) out of range of memory size %d reading %s",
			offset, mem.Size(ctx), fieldName))
	}
	return result
}

// requireWriteUint64Le is like api.Memory except that it panics if the offset
// is out of range.
func requireWriteUint64Le(ctx context.Context, mem api.Memory, fieldName string, offset uint32, val uint64) {
	if ok := mem.WriteUint64Le(ctx, offset, val); !ok {
		panic(fmt.Errorf("Memory.WriteUint64Le(ctx, %d, %d) out of range of memory size %d writing %s",
			offset, val, mem.Size(ctx), fieldName))
	}
}

// requireWriteUint32Le is like api.Memory except that it panics if the offset
// is out of range.
func requireWriteUint32Le(ctx context.Context, mem api.Memory, fieldName string, offset uint32, val uint32) {
	if ok := mem.WriteUint32Le(ctx, offset, val); !ok {
		panic(fmt.Errorf("Memory.WriteUint32Le(ctx, %d, %d) out of range of memory size %d writing %s",
			offset, val, mem.Size(ctx), fieldName))
	}
}
