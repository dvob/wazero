## WASI example

The examples in this directory show how to use I/O in your WebAssembly
modules using WASI (WebAssembly System Interface).

* [Rust](rust) - Built with `cargo build --release --target wasm32-wasi`
* [TinyGo](tinygo) - Built with `tinygo build -o cat.wasm -scheduler=none --no-debug -target=wasi cat.go`

Run the examples as follows:
```shell
# rust
go run cat.go rust/target/wasm32-wasi/release/cat.wasm test.txt

# tinygo
go run cat.go tinygo/cat.wasm test.txt
```
