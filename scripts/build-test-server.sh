#!/bin/bash
###
 # @Author: kamalyes 501893067@qq.com
 # @Date: 2025-12-31 19:50:00
 # @LastEditors: kamalyes 501893067@qq.com
 # @LastEditTime: 2025-12-31 19:50:00
 # @FilePath: \go-stress\scripts\build-test-server.sh
 # @Description: Build script for test server (wrapper for build-linux.sh)
 # 
 # Copyright (c) 2025 by kamalyes, All Rights Reserved. 
### 

set -e

# 获取脚本所在目录
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 导入通用函数
source "${SCRIPT_DIR}/common.sh"

# 默认设置测试服务器的构建参数
export BINARY_NAME="${BINARY_NAME:-test-server}"
export SOURCE_FILE="${SOURCE_FILE:-testserver/test_server.go}"

echo "🧪 构建测试服务器..."
echo "📁 源文件: ${SOURCE_FILE}"
echo "📦 输出名称: ${BINARY_NAME}"
echo ""

# 临时修改 build-linux.sh 的构建逻辑
# 保存所有参数并添加二进制名称
ARGS=("$@")

# 如果没有指定 binary-name，添加默认值
HAS_BINARY_NAME=false
for arg in "${ARGS[@]}"; do
    if [[ "$arg" == "--binary-name" ]]; then
        HAS_BINARY_NAME=true
        break
    fi
done

if [[ "$HAS_BINARY_NAME" == "false" ]]; then
    ARGS+=("--binary-name" "${BINARY_NAME}")
fi

# 构建函数 - 覆盖 build-linux.sh 的 build_target 函数
build_target() {
    local os=$1
    local arch=$2
    local output="${OUTPUT_DIR:-./deployments}/${BINARY_NAME}"
    
    # 如果是批量模式，添加平台后缀
    if [[ "${BATCH_MODE}" == "true" ]]; then
        output="${OUTPUT_DIR:-./deployments}/${BINARY_NAME}-${os}-${arch}"
    fi
    
    # Windows 平台需要添加 .exe 扩展名
    if [[ "${os}" == "windows" ]]; then
        output="${output}.exe"
    fi
    
    echo "🚀 正在构建测试服务器: ${output}"
    echo "📦 版本信息:"
    echo "   - Version: ${VERSION:-dev}"
    echo "   - Build Time: ${BUILD_TIME:-$(date -u '+%Y-%m-%d_%H:%M:%S')}"
    echo "   - Git Commit: ${GIT_COMMIT:-unknown}"
    echo "   - Platform: ${os}/${arch}"
    
    local LDFLAGS="-s -w -extldflags '-static' -X main.version=${VERSION:-dev} -X main.buildTime=${BUILD_TIME:-$(date -u '+%Y-%m-%d_%H:%M:%S')} -X main.gitCommit=${GIT_COMMIT:-unknown}"
    local BUILD_TAGS="netgo"
    local TRIM_PATH="-trimpath"
    
    if GOOS=${os} GOARCH=${arch} CGO_ENABLED=0 go build \
        ${TRIM_PATH} \
        -tags "${BUILD_TAGS}" \
        -ldflags "${LDFLAGS}" \
        -o ${output} ${SOURCE_FILE}; then
        echo "✅ 测试服务器构建成功: ${output}"
        
        # 显示文件大小
        if [[ "$OSTYPE" == "darwin"* ]]; then
            size=$(ls -lh ${output} | awk '{print $5}')
        else
            size=$(du -h ${output} | cut -f1)
        fi
        echo "📦 原始大小: ${size}"
        
        # 可选：使用 UPX 压缩
        if command -v upx &> /dev/null && [[ "${UPX_COMPRESS}" == "true" ]]; then
            echo "🗜️  使用 UPX 压缩..."
            upx --best --lzma ${output} 2>/dev/null || upx --best ${output}
            if [[ "$OSTYPE" == "darwin"* ]]; then
                compressed_size=$(ls -lh ${output} | awk '{print $5}')
            else
                compressed_size=$(du -h ${output} | cut -f1)
            fi
            echo "📦 压缩后大小: ${compressed_size}"
        fi
    else
        echo "❌ 测试服务器构建失败: ${output}"
        return 1
    fi
    echo ""
}

# 导出函数供 build-linux.sh 使用
export -f build_target

# 调用通用构建脚本，但使用我们自定义的 build_target 函数
bash "${SCRIPT_DIR}/build-linux.sh" "${ARGS[@]}"
