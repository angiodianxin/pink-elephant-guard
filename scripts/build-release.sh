#!/usr/bin/env sh
# L1 CLI pink-elephant-scan を 3 プラットフォームへクロスコンパイルし、
# SHA-256 チェックサムを生成する（design-claude.md §13.3 SHOULD、§13.6）。
#
# usage: scripts/build-release.sh [出力ディレクトリ]   （既定: dist）
#
# 成果物（出力ディレクトリ直下、すべて GitHub Releases へ添付する）:
#   pink-elephant-scan_linux_amd64
#   pink-elephant-scan_darwin_arm64
#   pink-elephant-scan_windows_amd64.exe
#   SHA256SUMS.txt                      # `sha256sum -c SHA256SUMS.txt` で検証できる
#
# CGO_ENABLED=0 の単一静的バイナリ（§13.3 MUST）。-trimpath と -ldflags="-s -w" は
# README「ビルド」節と同じ。出力ディレクトリは .gitignore 対象で、コミットしない（§13.1 MUST）。
set -eu

cd "$(dirname "$0")/.."

out=${1:-dist}
name=pink-elephant-scan

rm -rf "$out"
mkdir -p "$out"

build() {
	goos=$1
	goarch=$2
	suffix=$3
	echo "build: $goos/$goarch"
	(
		cd scan
		GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
			go build -trimpath -ldflags="-s -w" -o "../$out/${name}_${goos}_${goarch}${suffix}" .
	)
}

build linux   amd64 ""
build darwin  arm64 ""
build windows amd64 .exe

# チェックサムはファイル名だけを含める（ディレクトリ名を含めると利用者側で -c が通らない）。
(
	cd "$out"
	sha256sum "${name}"_* > SHA256SUMS.txt
	sha256sum -c SHA256SUMS.txt
)

echo "build: done -> $out/"
ls -l "$out"
