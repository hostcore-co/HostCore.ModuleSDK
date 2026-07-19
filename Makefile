# HostCore.ModuleSDK — developer tasks
#
#   make build    compile the example Pterodactyl module to WASM
#   make test     build the module and run the end-to-end verification harness
#   make verify   alias for test
#   make clean    remove build artifacts

MODULE_PKG ?= ./modules
MODULE_OUT ?= modules/pterodactyl.wasm

.PHONY: build test verify clean

build:
	@./scripts/build.sh $(MODULE_PKG) $(MODULE_OUT)

test: build
	@echo "› running verification harness"
	@cd test && go run .

verify: test

clean:
	@rm -f modules/pterodactyl.wasm
