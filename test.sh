#!/usr/bin/env bash
#
# test.sh - headless crawler 重构后的单元测试入口
#
# 用法:
#   ./test.sh              # 运行重构相关的全部单元测试 (默认)
#   ./test.sh pipeline     # 仅 pipeline 包 (channel 连接 / stage 语义)
#   ./test.sh crawler      # 仅 crawler 包 (stage / strategy / hooks / errors)
#   ./test.sh headless     # 仅 headless 门面包 (Hooks 别名 / SetHooks)
#   ./test.sh vet          # go vet 静态检查
#   ./test.sh race         # 带 -race 竞态检测运行核心包
#   ./test.sh all          # 上述全部 + go build ./...
#
# 说明: 依赖 chromium 浏览器或需要绑定本地 TCP 端口的集成测试
# (browser / captcha / capsolver / hybrid / parser/files) 无法在受限
# 沙箱内运行, 默认不执行。
set -euo pipefail

# 把 Go 构建缓存重定向到可写目录, 避免默认缓存目录只读导致的失败.
export GOCACHE="${GOCACHE:-/private/tmp/katana-gocache}"
mkdir -p "${GOCACHE}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${ROOT_DIR}"

PIPELINE_PKG="./pkg/engine/headless/crawler/pipeline/"
CRAWLER_PKG="./pkg/engine/headless/crawler/"
HEADLESS_PKG="./pkg/engine/headless/"
NORMALIZER_PKGS="./pkg/engine/headless/crawler/normalizer/..."

# 重构新增 / 修改的核心包.
CORE_PKGS=(
	"${PIPELINE_PKG}"
	"${CRAWLER_PKG}"
	"${HEADLESS_PKG}"
)

TIMEOUT="${TEST_TIMEOUT:-60s}"

run_pipeline() {
	echo "==> pipeline 单元测试 (channel / stage / skip / error / shutdown)"
	go test -timeout "${TIMEOUT}" -v "${PIPELINE_PKG}"
}

run_crawler() {
	echo "==> crawler 单元测试 (6 stages / NavigationStrategy 链 / hooks / errors)"
	go test -timeout "${TIMEOUT}" -v \
		-run 'TestRun|TestNavigation|TestDefault|TestNew_|TestSetNavigation|TestIsElement|TestClassify|TestCollect|TestGraphWriter|TestStage|TestPageFingerprint|TestSimHash|TestExecuteCrawlStateAction' \
		"${CRAWLER_PKG}"
}

run_headless() {
	echo "==> headless 门面单元测试 (Hooks / SetHooks / stage 常量)"
	go test -timeout "${TIMEOUT}" -v "${HEADLESS_PKG}"
}

run_race() {
	echo "==> 竞态检测 (-race)"
	go test -timeout "${TIMEOUT}" -race \
		"${PIPELINE_PKG}" "${CRAWLER_PKG}" "${HEADLESS_PKG}"
}

run_vet() {
	echo "==> go vet"
	go vet ./pkg/engine/headless/...
}

run_build() {
	echo "==> go build ./..."
	go build ./...
}

run_default() {
	for pkg in "${CORE_PKGS[@]}"; do
		echo "==> go test ${pkg}"
		go test -timeout "${TIMEOUT}" "${pkg}"
	done
	# normalizer / simhash 是 crawler 直接依赖, 一并回归.
	echo "==> go test ${NORMALIZER_PKGS}"
	go test -timeout "${TIMEOUT}" ${NORMALIZER_PKGS}
}

case "${1:-default}" in
	pipeline) run_pipeline ;;
	crawler)  run_crawler ;;
	headless) run_headless ;;
	race)     run_race ;;
	vet)      run_vet ;;
	all)
		run_vet
		run_build
		run_pipeline
		run_crawler
		run_headless
		run_race
		;;
	default) run_default ;;
	*)
		echo "未知参数: $1" >&2
		echo "可选: pipeline | crawler | headless | race | vet | all" >&2
		exit 2
		;;
esac

echo
echo "全部请求的测试已通过 ✅"
