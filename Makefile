# HostCore.ModuleSDK — developer tasks
#
#   make build    compile every example module under modules/* to WASM
#   make test     build the modules and run the end-to-end verification harness
#   make verify   alias for test
#   make clean    remove build artifacts

.PHONY: build test verify clean

build:
	@./scripts/build.sh

test: build
	@echo "› running verification harness"
	@cd test && go run .

verify: test

clean:
	@rm -f modules/*/*.wasm
