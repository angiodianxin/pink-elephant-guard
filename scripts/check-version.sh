#!/usr/bin/env sh
# Git タグ・.claude-plugin/plugin.json の version・CHANGELOG.md の一致を確認する
# （design-claude.md §13.6「バージョンは SemVer」、§18）。
#
# usage: scripts/check-version.sh [vX.Y.Z]
#
#   引数なし : plugin.json の version に対応する見出し `## [X.Y.Z]` が CHANGELOG.md に
#              あることを確認する（CI で毎回実行し、版とログの乖離を早期に検出する）。
#   タグ指定 : 上記に加え、タグが `v` + plugin.json の version と一致し、CHANGELOG の
#              該当見出しが確定済み（`Unreleased` でない）であることを確認する
#              （リリースワークフローが実行する）。
#
# 失敗時は理由を stderr へ出して exit 1。jq が必要。
set -eu

cd "$(dirname "$0")/.."

PLUGIN_JSON=.claude-plugin/plugin.json
CHANGELOG=CHANGELOG.md

fail() {
	echo "check-version: $*" >&2
	exit 1
}

command -v jq >/dev/null 2>&1 || fail "jq が見つかりません"
[ -f "$PLUGIN_JSON" ] || fail "$PLUGIN_JSON がありません"
[ -f "$CHANGELOG" ] || fail "$CHANGELOG がありません"

version=$(jq -r '.version // empty' "$PLUGIN_JSON")
[ -n "$version" ] || fail "$PLUGIN_JSON に version がありません"

# SemVer 2.0.0 の公式正規表現（https://semver.org/#is-there-a-suggested-regular-expression-regex-to-check-a-semver-string）
# を POSIX ERE へ移植したもの。プレリリース識別子は空でないドット区切りで、数値のみの識別子は
# 先頭ゼロ不可（`1.0.0-01` / `1.0.0-alpha..1` は不正）。ビルドメタデータも空でないドット区切り（`1.0.0+build.` は不正）。
ident='(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)'
build='[0-9A-Za-z-]+'
semver="^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(-$ident(\\.$ident)*)?(\\+$build(\\.$build)*)?\$"
echo "$version" | grep -Eq "$semver" || fail "$PLUGIN_JSON の version は SemVer ではありません: $version"

# `## [X.Y.Z]` または `## [X.Y.Z] - <日付など>` の見出しを探す。
# `.` は正規表現でリテラルに扱えるようエスケープする。
escaped=$(printf '%s' "$version" | sed 's/[.+]/\\&/g')
heading=$(grep -E "^## \[$escaped\]( - .*)?\$" "$CHANGELOG" | head -n 1 || true)
[ -n "$heading" ] || fail "$CHANGELOG に見出し '## [$version]' がありません（plugin.json の version と一致させてください）"

if [ $# -ge 1 ]; then
	tag=$1
	expected="v$version"
	[ "$tag" = "$expected" ] || fail "タグ $tag が $PLUGIN_JSON の version と一致しません（期待: $expected）"
	if echo "$heading" | grep -iq 'unreleased'; then
		fail "$CHANGELOG の '$heading' が未確定です。リリース日に置き換えてからタグを打ってください"
	fi
	echo "check-version: OK ($tag = plugin.json $version = CHANGELOG '$heading')"
else
	echo "check-version: OK (plugin.json $version = CHANGELOG '$heading')"
fi
