# Changelog

本プロジェクトの変更履歴。書式は [Keep a Changelog](https://keepachangelog.com/ja/1.1.0/)、
版は [SemVer](https://semver.org/lang/ja/) に従う（`design-claude.md` §13.6、§18）。

`.claude-plugin/plugin.json` の `version`・Git タグ `vX.Y.Z`・本ファイルの見出し `## [X.Y.Z]` は
常に一致させる。一致は `scripts/check-version.sh` が CI とリリース時に検査する。
リリース時は該当見出しの `Unreleased` をリリース日（YYYY-MM-DD）へ置き換えてからタグを打つ。

## [Unreleased]

## [0.1.0] - Unreleased

### Added

- L3 スキル `skills/pink-elephant-guard/SKILL.md`（3 層統括版: 工程 1→2→3→4a→4b→5、PASS は L1/L2 の結果からのみ導く。#10）と仕様書 `design-claude.md`
- 媒体別要件の詳細 `skills/pink-elephant-guard/references/media-requirements.md`。SKILL.md 本体は要点と参照だけを残し、媒体を確定した時点で該当節を読む（#11）
- manifest スキーマの正本 `schema/manifest.schema.json`
- L1 CLI `pink-elephant-scan`（`scan/`）: 却下語の字面再侵入（Literal leak）を決定論的に検査する（#7）
  - 正規化パイプライン（NFKC → 小文字化 → カタカナ→ひらがな）
  - `visible_exceptions` の回数判定と例外マスク
  - 終了コード 0=PASS / 1=FAIL / 2=usage / 3=manifest / 4=draft / 5=internal（`scan/DESIGN.md` §2.2）
- 既知の漏れサンプルによる回帰テストスイート `scan/scan_test.go`（#8）
- CI（`go vet` / `go test` / `gofmt` / クロスコンパイル確認）と、`v*` タグでの
  3 プラットフォームバイナリ＋SHA-256 チェックサムの Releases 添付（#1）
