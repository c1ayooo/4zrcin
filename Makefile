# 4zrcin · 常用命令（Windows 上可用 git-bash / WSL / make，或直接用 scripts/ 里的脚本）
#
#   make gate      本地 gate：与 scripts/gate.ps1 / scripts/gate.sh 同一套检查
#                  （Go 构建/静态检查/测试、前端 typecheck/test/build、嵌入产物新鲜度、
#                  文档链接、harness 构建、控制面二进制、构建清单）
#   make manifest  只生成构建清单 -> .build/build-manifest.json（不做检查/构建）
#   make harness   从仓库外的 harness checkout 构建执行器 -> bin/cyber-harness + bin/cyber-scan
#                  （源码不在本仓库：HARNESS_DIR，默认 .build/harness-src）
#   make repair    构建离线游标修复工具 -> bin/zrcin-repair（用法见 README「离线修复交接游标」）
#   make radar     上游雷达：抓取 kksty/tianwu 的 main，生成新增提交日报 -> logs/upstream-radar/
#   make web       构建控制台前端 -> internal/web/static（产物入库，跑控制面的人不需要 Node）
#   make run       从源码启动控制台（需先 make harness）
#   make test      跑全部测试
#   make race      数据竞争检测（需 cgo/C 工具链；Windows 本机无 gcc 时用 WSL/CI）
#   make fmt       格式化

BIN     ?= bin
GATE_ARGS ?=
TARGET_OS := $(shell go env GOOS)
EXE     := $(if $(filter windows,$(TARGET_OS)),.exe,)
TAGS    := forceposix emptytemplates noembed osusergo full sqlite $(if $(filter windows,$(TARGET_OS)),,netgo)

# 本仓库不内置 harness 源码；把它单独 checkout 到这里或任意目录，再用
# `make harness HARNESS_DIR=<路径>` 指过来。
HARNESS_DIR ?= .build/harness-src

.PHONY: gate manifest harness scan-capture repair radar web run test race fmt clean

# 本地 gate 的单一实现在 cmd/zrcin-gate；这里只是又一层薄入口（脚本是另外两个）。
gate:
	go run ./cmd/zrcin-gate $(GATE_ARGS)

manifest:
	go run ./cmd/zrcin-gate -manifest-only $(GATE_ARGS)

harness:
	@test -f "$(HARNESS_DIR)/go.mod" || { \
	  echo "找不到 $(HARNESS_DIR)/go.mod：本仓库不内置 harness 源码。" >&2; \
	  echo "先单独 clone c1ayooo/cyber-harness 的 tianwu/fixes，再用 HARNESS_DIR 指过去，例如：" >&2; \
	  echo "  git clone --branch tianwu/fixes https://github.com/c1ayooo/cyber-harness.git .build/harness-src" >&2; \
	  echo "  make harness HARNESS_DIR=.build/harness-src" >&2; \
	  exit 1; \
	}
	cd "$(HARNESS_DIR)" && go build -tags "$(TAGS)" -o "$(CURDIR)/$(BIN)/cyber-harness$(EXE)" ./cmd/cyber-harness
	cd "$(HARNESS_DIR)" && go build -tags "$(filter-out full,$(TAGS))" -o "$(CURDIR)/$(BIN)/cyber-scan$(EXE)" ./cmd/cyber-scan
	@echo "ok: $(BIN)/cyber-harness$(EXE) + cyber-scan$(EXE)"

# capture 配方（docs/04 §3.3）：围栏流量节点——扫描引擎、发现组与内嵌模板
# bundle 全部裁掉；保留代理、guardrail（含保底审计）与证据 sidecar。与
# build-harness.* 一致地总是产出两个 scan 产物，供构建清单做体积对比。
scan-capture:
	@test -f "$(HARNESS_DIR)/go.mod" || { \
	  echo "找不到 $(HARNESS_DIR)/go.mod：先 make harness 或设置 HARNESS_DIR。" >&2; \
	  exit 1; \
	}
	cd "$(HARNESS_DIR)" && go build -tags "$(filter-out full,$(TAGS)) emptyresources captureonly" -o "$(CURDIR)/$(BIN)/cyber-scan-capture$(EXE)" ./cmd/cyber-scan
	@echo "ok: $(BIN)/cyber-scan-capture$(EXE)"

# 离线游标修复工具：停掉 zrcin 且项目暂停后，对单个方向显式预览/提交消费游标。
repair:
	go build -o "$(BIN)/zrcin-repair$(EXE)" ./cmd/zrcin-repair
	@echo "ok: $(BIN)/zrcin-repair$(EXE)"

# 上游雷达：跑一次就得到一份日报（新增提交、积压、冲突热区、敏感改动）。
# 日报落在 logs/upstream-radar/（已 gitignore），last-seen 记录下次对比的基线。
radar:
	./scripts/upstream-radar.sh

# 前端产物是 go:embed 的来源，所以它入库；只有改界面时才需要 Node。
web:
	pnpm install
	pnpm build
	@echo "ok: internal/web/static"

run:
	go run ./cmd/zrcin --addr 127.0.0.1:8080 --data ./data --harness ./$(BIN)/cyber-harness$(EXE) --scan ./$(BIN)/cyber-scan$(EXE)

test:
	go test ./... -p 1 -count=1

# 数据竞争检测：-race 需要 cgo/C 工具链（Linux gcc / Windows MinGW-w64 gcc）。
# 缺编译器时给出可读指引，而不是一个难懂的 cgo 错误。
RACE_PKGS ?= ./internal/api ./internal/lifecycle ./internal/verifier ./internal/store ./internal/decider ./internal/board ./internal/collab

race:
	@command -v "$$(go env CC)" >/dev/null 2>&1 || { \
	  echo "race 检测需要 C 工具链（cgo）：本机没有 $$(go env CC)。" >&2; \
	  echo "请用 WSL/Docker/Linux 跑 make race，或安装 MinGW-w64（见 CONTRIBUTING）" >&2; \
	  exit 1; \
	}
	CGO_ENABLED=1 go test -race -count=1 -timeout 30m $(RACE_PKGS)

fmt:
	gofmt -w cmd internal

clean:
	rm -rf $(BIN)
