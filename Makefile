.PHONY: run build setup tidy test gum-demo ramdisk

# macOS 15 APFS bug: F_PREALLOCATE fails with EILSEQ for binaries >=5M (see Go issue)
# Workaround: build on HFS+ RAM disk where fcntl(F_PREALLOCATE) and mmap succeed
RAMDISK := /tmp/ramdisk
RAMDISK_DMG := /tmp/ramdisk.dmg
UV_CACHE := $(RAMDISK)/uv-cache
HF_CACHE := $(RAMDISK)/hf-home

ramdisk:
	@if ! mount | grep -qE 'on (/private)?$(RAMDISK) '; then \
		echo "-> creating 3GB HFS+ RAM disk for builds & caches (workaround macOS APFS EILSEQ)"; \
		mkdir -p $(RAMDISK); \
		if [ ! -f "$(RAMDISK_DMG)" ]; then \
			hdiutil create -size 3g -fs "HFS+" -volname RAMDISK $(RAMDISK_DMG) >/dev/null 2>&1 || true; \
		fi; \
		hdiutil attach $(RAMDISK_DMG) -mountpoint $(RAMDISK) >/dev/null 2>&1 || true; \
	fi
	@mkdir -p $(UV_CACHE) $(RAMDISK)/uv-tools $(HF_CACHE)
	@echo "✓ RAM disk at $(RAMDISK)"

setup: ramdisk
	@TMPDIR=$(RAMDISK) UV_CACHE_DIR=$(UV_CACHE) HF_HOME=$(HF_CACHE) bash ./scripts/setup.sh

run: ramdisk
	TMPDIR=$(RAMDISK) UV_CACHE_DIR=$(UV_CACHE) HF_HOME=$(HF_CACHE) go run ./cmd/mimir $(URL)

build: ramdisk
	@mkdir -p ./bin
	@rm -f ./bin/mimir $(RAMDISK)/mimir
	TMPDIR=$(RAMDISK) UV_CACHE_DIR=$(UV_CACHE) HF_HOME=$(HF_CACHE) go build -o $(RAMDISK)/mimir ./cmd/mimir
	cat $(RAMDISK)/mimir > ./bin/mimir && chmod +x ./bin/mimir
	@echo "✓ bin/mimir — built via $(RAMDISK) workaround"

tidy: ramdisk
	TMPDIR=$(RAMDISK) UV_CACHE_DIR=$(UV_CACHE) HF_HOME=$(HF_CACHE) go mod tidy
	go fmt ./...

test: ramdisk
	TMPDIR=$(RAMDISK) UV_CACHE_DIR=$(UV_CACHE) HF_HOME=$(HF_CACHE) go test ./... -v

# gum-powered quick launcher (no TUI, just gum)
gum-demo:
	@gum style --border rounded --padding "1 2" --border-foreground 205 "o MIMIR" "Von background answer finder"
	@gum confirm "Extract question from Von now?" && echo "extracting..." || echo "cancelled"

clean-ramdisk:
	hdiutil detach $(RAMDISK) 2>/dev/null || true
	rm -f $(RAMDISK_DMG)
	rm -rf $(RAMDISK)

