package spectest

import (
	"embed"
	"fmt"
	"math"
	"path"
	"runtime"
	"strings"
	"testing"

	"github.com/tetratelabs/wazero/internal/engine/compiler"
	"github.com/tetratelabs/wazero/internal/engine/interpreter"
	"github.com/tetratelabs/wazero/internal/integration_test/spectest"
	"github.com/tetratelabs/wazero/internal/platform"
	"github.com/tetratelabs/wazero/internal/wasm"
)

//go:embed testdata/*.wasm
//go:embed testdata/*.json
var testcases embed.FS

const enabledFeatures = wasm.Features20220419

func TestCompiler(t *testing.T) {
	if !platform.CompilerSupported() {
		t.Skip()
	}

	spectest.Run(t, testcases, compiler.NewEngine, enabledFeatures, func(jsonname string) bool {
		// TODO: remove after SIMD proposal
		if strings.Contains(jsonname, "simd") {
			switch path.Base(jsonname) {
			case "simd_address.json", "simd_const.json", "simd_align.json", "simd_load16_lane.json", "simd_load32_lane.json",
				"simd_load64_lane.json", "simd_load8_lane.json", "simd_lane.json", "simd_load_extend.json",
				"simd_load_splat.json", "simd_load_zero.json", "simd_store.json", "simd_store16_lane.json",
				"simd_store32_lane.json", "simd_store64_lane.json", "simd_store8_lane.json":
				return true
			case "simd_bitwise.json", "simd_boolean.json", "simd_bit_shift.json",
				"simd_i8x16_cmp.json", "simd_i16x8_cmp.json", "simd_i32x4_cmp.json", "simd_i64x2_cmp.json",
				"simd_f32x4_cmp.json", "simd_f64x2_cmp.json":
				// TODO: implement on arm64.
				return runtime.GOARCH == "amd64"
			default:
				return false // others not supported, yet!
			}
		}
		return true
	})
}

func TestInterpreter(t *testing.T) {
	v := math.MinInt8
	uv := byte(v)
	fmt.Printf("%x\n", uv)

	spectest.Run(t, testcases, interpreter.NewEngine, enabledFeatures, func(jsonname string) bool {
		// TODO: remove after SIMD proposal
		if strings.Contains(jsonname, "simd") {
			switch path.Base(jsonname) {
			case "simd_address.json", "simd_const.json", "simd_align.json", "simd_load16_lane.json",
				"simd_load32_lane.json", "simd_load64_lane.json", "simd_load8_lane.json", "simd_lane.json",
				"simd_load_extend.json", "simd_load_splat.json", "simd_load_zero.json", "simd_store.json",
				"simd_store16_lane.json", "simd_store32_lane.json", "simd_store64_lane.json", "simd_store8_lane.json",
				"simd_bitwise.json", "simd_boolean.json", "simd_bit_shift.json", "simd_i8x16_cmp.json", "simd_i16x8_cmp.json",
				"simd_i32x4_cmp.json", "simd_i64x2_cmp.json", "simd_f32x4_cmp.json", "simd_f64x2_cmp.json",
				"simd_f32x4_arith.json", "simd_f64x2_arith.json", "simd_i16x8_arith.json", "simd_i64x2_arith.json",
				"simd_i32x4_arith.json", "simd_i8x16_arith.json", "simd_i16x8_sat_arith.json", "simd_i8x16_sat_arith.json":
				return false
			case
				"simd_f32x4.json",
				"simd_f64x2.json",
				"simd_i16x8_arith2.json",
				"sid_i8x16_arith2.json",
				"simd_i32x4_arith2.json",
				"simd_i64x2_arith2.json":
				return false
			default:
				return false // others not supported, yet!
			}
		}
		return false
	})
}
