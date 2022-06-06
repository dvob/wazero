// Package wasm_exec contains imports and state needed by wasm go compiles when
// GOOS=js and GOARCH=wasm.
//
// See /wasm_exec/REFERENCE.md for a deeper dive.
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
	g := &jsWasm{}
	return b.r.NewModuleBuilder("go").
		ExportFunction("runtime.wasmExit", g._wasmExit).
		ExportFunction("runtime.wasmWrite", g._wasmWrite).
		ExportFunction("runtime.resetMemoryDataView", g._resetMemoryDataView).
		ExportFunction("runtime.nanotime1", g._nanotime1).
		ExportFunction("runtime.walltime", g._walltime).
		ExportFunction("runtime.scheduleTimeoutEvent", g._scheduleTimeoutEvent).
		ExportFunction("runtime.clearTimeoutEvent", g._clearTimeoutEvent).
		ExportFunction("runtime.getRandomData", g._getRandomData)
}

// Compile implements Builder.Compile
func (b *builder) Compile(ctx context.Context, config wazero.CompileConfig) (wazero.CompiledModule, error) {
	return b.moduleBuilder().Compile(ctx, config)
}

// Instantiate implements Builder.Instantiate
func (b *builder) Instantiate(ctx context.Context, ns wazero.Namespace) (api.Closer, error) {
	return b.moduleBuilder().Instantiate(ctx, ns)
}

// jsWasm holds defines the "go" imports used by wasm_exec.
//
// Note: This is module-scoped, so only safe when used in a wazero.Namespace
// that only instantiates one module.
type jsWasm struct {
	mux                   sync.RWMutex
	nextCallbackTimeoutID uint32                 // guarded by mux
	scheduledTimeouts     map[uint32]*time.Timer // guarded by mux

	closed *uint64
}

// _wasmExit converts the GOARCH=wasm stack to be compatible with api.ValueType
// in order to call wasmExit.
func (j *jsWasm) _wasmExit(ctx context.Context, mod api.Module, sp uint32) {
	code := requireReadUint32Le(ctx, mod.Memory(), "code", sp+8)
	j.wasmExit(ctx, mod, code)
}

// wasmExit implements runtime.wasmExit which supports runtime.exit.
//
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/sys_wasm.go#L28
func (j *jsWasm) wasmExit(ctx context.Context, mod api.Module, code uint32) {
	closed := uint64(1) + uint64(code)<<32 // Store exitCode as high-order bits.
	if !atomic.CompareAndSwapUint64(j.closed, 0, closed) {
		return
	}

	// TODO: free resources for this module
	_ = mod.CloseWithExitCode(ctx, code)
}

// _wasmWrite converts the GOARCH=wasm stack to be compatible with
// api.ValueType in order to call wasmWrite.
func (j *jsWasm) _wasmWrite(ctx context.Context, mod api.Module, sp uint32) {
	fd := requireReadUint64Le(ctx, mod.Memory(), "fd", sp+8)
	p := requireReadUint64Le(ctx, mod.Memory(), "p", sp+16)
	n := requireReadUint32Le(ctx, mod.Memory(), "n", sp+24)
	j.wasmWrite(ctx, mod, fd, p, n)
}

// wasmWrite implements runtime.wasmWrite which supports runtime.write and
// runtime.writeErr. It is only known to be used with fd = 2 (stderr).
//
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/os_js.go#L29
func (j *jsWasm) wasmWrite(ctx context.Context, mod api.Module, fd, p uint64, n uint32) {
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

// _resetMemoryDataView converts the GOARCH=wasm stack to be compatible with
// api.ValueType in order to call resetMemoryDataView.
func (j *jsWasm) _resetMemoryDataView(ctx context.Context, mod api.Module, sp uint32) {
	j.resetMemoryDataView(ctx, mod)
}

// resetMemoryDataView signals wasm.OpcodeMemoryGrow happened, indicating any
// cached view of memory should be reset.
//
// See https://github.com/golang/go/blob/9839668b5619f45e293dd40339bf0ac614ea6bee/src/runtime/mem_js.go#L82
func (j *jsWasm) resetMemoryDataView(ctx context.Context, mod api.Module) {
	// TODO: Compiler-based memory.grow callbacks are ignored until we have a generic solution #601
}

// _nanotime1 converts the GOARCH=wasm stack to be compatible with
// api.ValueType in order to call nanotime1.
func (j *jsWasm) _nanotime1(ctx context.Context, mod api.Module, sp uint32) {
	nanos := j.nanotime1(ctx, mod)
	requireWriteUint64Le(ctx, mod.Memory(), "t", sp+8, nanos)
}

// nanotime1 implements runtime.nanotime which supports time.Since.
//
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/sys_wasm.s#L184
func (j *jsWasm) nanotime1(ctx context.Context, mod api.Module) uint64 {
	return uint64(getSysCtx(mod).Nanotime(ctx))
}

// _walltime converts the GOARCH=wasm stack to be compatible with
// api.ValueType in order to call walltime.
func (j *jsWasm) _walltime(ctx context.Context, mod api.Module, sp uint32) {
	sec, nsec := j.walltime(ctx, mod)
	requireWriteUint64Le(ctx, mod.Memory(), "sec", sp+8, uint64(sec))
	requireWriteUint32Le(ctx, mod.Memory(), "nsec", sp+16, uint32(nsec))
}

// walltime implements runtime.walltime which supports time.Now.
//
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/sys_wasm.s#L188
func (j *jsWasm) walltime(ctx context.Context, mod api.Module) (uint64 int64, uint32 int32) {
	return getSysCtx(mod).Walltime(ctx)
}

// _scheduleTimeoutEvent converts the GOARCH=wasm stack to be compatible with
// api.ValueType in order to call scheduleTimeoutEvent.
func (j *jsWasm) _scheduleTimeoutEvent(ctx context.Context, mod api.Module, sp uint32) {
	delayMs := requireReadUint64Le(ctx, mod.Memory(), "delay", sp+8)
	id := j.scheduleTimeoutEvent(ctx, mod, delayMs)
	requireWriteUint32Le(ctx, mod.Memory(), "id", sp+16, id)
}

// scheduleTimeoutEvent implements runtime.scheduleTimeoutEvent which supports
// runtime.notetsleepg used by runtime.signal_recv.
//
// Unlike other most functions prefixed by "runtime.", this both launches a
// goroutine and invokes code compiled into wasm "resume".
//
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/sys_wasm.s#L192
func (j *jsWasm) scheduleTimeoutEvent(ctx context.Context, mod api.Module, delayMs uint64) uint32 {
	delay := time.Duration(delayMs) * time.Millisecond

	resume := mod.ExportedFunction("resume")

	// Invoke resume as an anonymous function, to propagate the context.
	callResume := func() {
		if err := j.failIfClosed(mod); err != nil {
			return
		}
		// While there's a possible error here, panicking won't help as it is
		// on a different goroutine.
		_, _ = resume.Call(ctx)
	}

	return j.scheduleEvent(delay, callResume)
}

// _clearTimeoutEvent converts the GOARCH=wasm stack to be compatible with
// api.ValueType in order to call clearTimeoutEvent.
func (j *jsWasm) _clearTimeoutEvent(ctx context.Context, mod api.Module, sp uint32) {
	id := requireReadUint32Le(ctx, mod.Memory(), "id", sp+8)
	j.clearTimeoutEvent(id)
}

// clearTimeoutEvent implements runtime.clearTimeoutEvent which supports
// runtime.notetsleepg used by runtime.signal_recv.
//
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/sys_wasm.s#L196
func (j *jsWasm) clearTimeoutEvent(id uint32) {
	if t := j.removeEvent(id); t != nil {
		if !t.Stop() {
			<-t.C
		}
	}
}

// _getRandomData converts the GOARCH=wasm stack to be compatible with
// api.ValueType in order to call getRandomData.
func (j *jsWasm) _getRandomData(ctx context.Context, mod api.Module, sp uint32) {
	buf := uint32(requireReadUint64Le(ctx, mod.Memory(), "buf", sp+8))
	bufLen := uint32(requireReadUint64Le(ctx, mod.Memory(), "bufLen", sp+16))

	j.getRandomData(ctx, mod, buf, bufLen)
}

// getRandomData implements runtime.getRandomData, which initializes the seed
// for runtime.fastrand.
//
// See https://github.com/golang/go/blob/4170084ad12c2e14dc0485d2a17a838e97fee8c7/src/runtime/sys_wasm.s#L200
func (j *jsWasm) getRandomData(ctx context.Context, mod api.Module, buf, bufLen uint32) {
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
func (j *jsWasm) removeEvent(id uint32) *time.Timer {
	j.mux.Lock()
	defer j.mux.Unlock()

	t, ok := j.scheduledTimeouts[id]
	if ok {
		delete(j.scheduledTimeouts, id)
		return t
	}
	return nil
}

// failIfClosed returns a sys.ExitError if wasmExit was called.
func (j *jsWasm) failIfClosed(mod api.Module) error {
	if closed := atomic.LoadUint64(j.closed); closed != 0 {
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
func (j *jsWasm) scheduleEvent(d time.Duration, f func()) uint32 {
	j.mux.Lock()
	defer j.mux.Unlock()

	id := j.nextCallbackTimeoutID
	j.nextCallbackTimeoutID++
	j.scheduledTimeouts[id] = time.AfterFunc(d, f)
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
