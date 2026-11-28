# 4zrcin · 常用命令（Windows 上可用 git-bash / WSL / make，或直接用 scripts/ 里的脚本）
#
#   make gate         本地 gate：与 scripts/gate.ps1 / scripts/gate.sh 同一套检查
#                     （Go 构建/静态检查/测试、前端 typecheck/test/build、嵌入产物新鲜度、
#                     文档链接、harness 构建、控制面二进制、构建清单）
#   make manifest     只生成构建清单 -> .build/build-manifest.json（不做检查/构建）
#   make harness      转发到 harness 检出的 engine + scan 目标 -> bin/cyber-harness + bin/cyber-scan
#                     （按需的调试构建：独立 Hub 已退役，不进发布包，docs/19 已决 28；
#                      源码不在本仓库：HARNESS_DIR，默认 .build/harness-src）
#   make zrcin        构建控制面主程序 -> bin/zrcin（与 gate 构建它的那条命令一致）
#   make node         构建隧道启动器 -> bin/zrcin-node（-tags remtunnel）
#   make rem-relay    构建 rem 中继 -> bin/rem-relay
#   make repair       构建离线游标修复工具 -> bin/zrcin-repair（用法见 README「离线修复交接游标」）
#   make radar        上游雷达：抓取 kksty/tianwu 的 main，生成新增提交日报 -> logs/upstream-radar/
#   make web          构建控制台前端 -> internal/web/bundle（产物入库，跑控制面的人不需要 Node）
#   make run          从源码启动控制台（需先 make harness）
#   make test         跑全部测试
#   make race         数据竞争检测（需 cgo/C 工具链；Windows 本机无 gcc 时用 WSL/CI）
#   make fmt          格式化

BIN     ?= bin
GATE_ARGS ?=
TARGET_OS := $(shell go env GOOS)
EXE     := $(if $(filter windows,$(TARGET_OS)),.exe,)
# `full` 退役（docs/19 已决 28）：独立 Hub 不进发布包，gate 的 harness 步只
# 构建 scan（引擎的 scan 目标本就 filter-out full）；`make harness` 保留为
# 按需的调试构建。
TAGS    := forceposix emptytemplates noembed osusergo sqlite $(if $(filter windows,$(TARGET_OS)),,netgo)

# 本仓库不内置 harness 源码；把它单独 checkout 到这里或任意目录，再用
# `make harness HARNESS_DIR=<路径>` 指过来。构建配方收敛在 harness 仓自己的
# Makefile（docs/19 §7），这里只转发标签串和输出目录。
HARNESS_DIR ?= .build/harness-src

.PHONY: gate manifest harness zrcin test-inprochost node rem-relay repair radar web run test race fmt clean

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
	cd "$(HARNESS_DIR)" && make engine scan BIN_DIR="$(CURDIR)/$(BIN)" TAGS="$(TAGS)"
	@echo "ok: $(BIN)/cyber-harness$(EXE) + cyber-scan$(EXE)"

# 控制面主程序：与 gate（cmd/zrcin-gate）构建 bin/zrcin 的那条命令一致。
# 标签探测也照抄 gate：inprochost 条件于 harness 检出可解析，remtunnel
# 条件于同级 ../rem 存在；两者皆无时同 gate 一样退回默认构建图。
ZRCIN_TAGS :=
ifneq ($(or $(wildcard $(HARNESS_DIR)/go.mod),$(wildcard ../cyber-harness/go.mod)),)
ZRCIN_TAGS := inprochost
endif
ifneq ($(wildcard ../rem),)
ifdef ZRCIN_TAGS
ZRCIN_TAGS := $(ZRCIN_TAGS),remtunnel
else
ZRCIN_TAGS := remtunnel
endif
endif

zrcin:
	go build $(if $(ZRCIN_TAGS),-tags "$(ZRCIN_TAGS)") -o "$(BIN)/zrcin$(EXE)" ./cmd/zrcin
	@echo "ok: $(BIN)/zrcin$(EXE)"

# 内嵌构建的单元测试（docs/19 P6）：inprochost 标签下的代码在默认测试（不带
# 标签）里根本不编译，这里按 zrcin 目标同一套标签探测跑一遍；没有引擎检出
# 时如实跳过，不假装测过。
test-inprochost:
ifeq ($(strip $(ZRCIN_TAGS)),)
	@echo "skip: 没有 harness 检出（.build/harness-src 或同级 cyber-harness），没有内嵌构建可测"
else
	go test -count=1 -tags "$(ZRCIN_TAGS)" ./cmd/zrcin ./internal/host/...
	@echo "ok: test-inprochost（-tags $(ZRCIN_TAGS)）"
endif

# 隧道启动器（docs/14 §7.1）：ingress 下发给节点侧的入口二进制。
node:
	go build -tags remtunnel -o $(BIN)/zrcin-node$(EXE) ./cmd/zrcin-node
	@echo "ok: $(BIN)/zrcin-node$(EXE)"

rem-relay:
	go build -o $(BIN)/rem-relay$(EXE) ./cmd/rem-relay
	@echo "ok: $(BIN)/rem-relay$(EXE)"

# 离线游标修复工具：停掉 zrcin 且项目暂停后，对单个方向显式预览/提交消费游标。
repair:
	go build -o "$(BIN)/zrcin-repair$(EXE)" ./cmd/zrcin-repair
	@echo "ok: $(BIN)/zrcin-repair$(EXE)"

# 上游雷达：跑一次就得到一份日报（新增提交、积压、冲突热区、敏感改动）。
# 日报落在 logs/upstream-radar/（已 gitignore），last-seen 记录下次对比的基线。
radar:
	./scripts/upstream-radar.sh

# 前端源码在 4zweb 检出（docs/19 P1-3，同父目录或 ZRCIN_WEB_DIR）。
# 产物 internal/web/bundle 入库：go:embed 读它，gate 校验新鲜度。
WEB_DIR ?= $(patsubst %/,%,$(dir $(abspath ../4zweb)))
web:
	cd $(WEB_DIR) && pnpm install --frozen-lockfile && pnpm build
	# The generator resolves its own dist directory (ZRCIN_WEB_DIST, then the
	# sibling checkout). Hand it the checkout that was just built, or WEB_DIR
	# rebuilds one frontend and embeds another.
	ZRCIN_WEB_DIR='$(WEB_DIR)' go generate ./internal/web
	@echo "ok: internal/web/bundle"

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
