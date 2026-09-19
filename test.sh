#!/usr/bin/env bash
#
# test.sh — katana 单元测试脚本（手动执行）
#
# 用法:
#   ./test.sh            # 运行全部单元测试
#   ./test.sh headless   # 只运行 headless 引擎相关测试
#   ./test.sh crawler    # 只运行 crawler 包测试（Pipeline / Strategy / Hooks）
#   ./test.sh build      # 只编译，不运行测试
#
# 说明:
#   - 本脚本只运行单元测试（go test），不触发 integration_tests/（需要真实浏览器与网络）。
#   - pkg/engine/headless/captcha/capsolver 的测试需要监听本地端口（httptest），
#     在沙箱/受限网络环境下可能失败，属环境限制而非代码问题。

set -euo pipefail
cd "$(dirname "$0")"

# 受限环境下 Go 构建缓存可能不可写，允许通过环境变量覆盖
export GOCACHE="${GOCACHE:-$(go env GOCACHE)}"

TARGET="${1:-all}"

run() {
    echo ""
    echo "==> $*"
    "$@"
}

case "$TARGET" in
build)
    run go build ./...
    ;;
crawler)
    run go test -v -count=1 ./pkg/engine/headless/crawler/
    ;;
headless)
    run go test -v -count=1 ./pkg/engine/headless/ ./pkg/engine/headless/crawler/...
    ;;
all)
    run go build ./...
    run go vet ./pkg/engine/headless/...
    # 重点：重构涉及的包（Pipeline / Strategy / Hooks）
    run go test -v -count=1 ./pkg/engine/headless/crawler/
    run go test -v -count=1 ./pkg/engine/headless/
    # 全量单元测试
    run go test -count=1 ./...
    ;;
*)
    echo "unknown target: $TARGET (expected: all | build | crawler | headless)" >&2
    exit 2
    ;;
esac

echo ""
echo "==> DONE ($TARGET)"
