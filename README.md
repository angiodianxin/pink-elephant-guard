# Pink Elephant Guard

却下・削除・訂正・禁止された要素を変更履歴として隔離し、修正後の現在状態だけから
完成物を組み直すための Claude Code スキル。

仕様は [`design-claude.md`](design-claude.md) が正本。構成は 3 層に分かれる。

| 層 | 実体 | 役割 | 状態 |
|---|---|---|---|
| L1 | `scan/`（Go CLI `pink-elephant-scan`） | 却下語の字面再侵入（Literal leak）を決定論的に検査する。トークン消費 0 | テスト先行（実装は #7） |
| L2 | Haiku サブエージェント | 意味検査（同義語・上位語・言い換え） | 未着手 |
| L3 | [`skills/pink-elephant-guard/SKILL.md`](skills/pink-elephant-guard/SKILL.md) | 統括・manifest 作成・再生成 | — |

`pink-elephant-manifest.json` は L1/L2/L3 間の中間生成物で、スキーマの正本は
[`schema/manifest.schema.json`](schema/manifest.schema.json)。利用時に生成される
実ファイルはユーザー内容を含み得るため、コミットしない（`.gitignore` 済み）。

## 動作要件

- Claude Code（スキルとしての利用）
- Go 1.22+ — **L1 CLI のビルド時のみ**必要。配布バイナリを使う場合は不要

## L1 CLI: pink-elephant-scan

> **現在 `scan/` にはテストのみが入っており、実装（`normalize.go` / `scan.go` / `main.go`）は
> 未着手です（[#7](https://github.com/angiodianxin/pink-elephant-guard/issues/7)）。
> そのため `go test ./scan/...` は現時点ではビルドエラーで落ちます。これは TDD の red 段階として
> 意図した状態です。**

以下は `scan/scan_test.go` が固定している、実装が満たすべき外形契約。

```text
usage: pink-elephant-scan --manifest <path> --draft <path>
```

| exit | 意味 |
|---|---|
| 0 | PASS（hit 0 件）。stdout に `{"pass":true,"hits":[]}` |
| 1 | FAIL（hit 1 件以上）。stdout に hit の一覧 |
| 2 | 入力不正（manifest 不正・引数不正・ファイル読取り不可）。stdout には何も出さない |

詳細な入出力契約は [`scan/DESIGN.md`](scan/DESIGN.md) を参照。

### ビルド（実装後）

```sh
cd scan
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../bin/pink-elephant-scan .
```

成果物は `bin/`（`.gitignore` 対象）へ置く。バイナリはコミットせず、
GitHub Releases + SHA-256 チェックサムで配布する。

`scan/go.mod` は現在テストだけを含むため依存が無い状態（`go mod tidy` 済み）。
`normalize.go` を実装する際に、設計で唯一許可されている外部依存を追加する。

```sh
cd scan
go get golang.org/x/text   # unicode/norm のみ使用。これ以外の外部依存を追加してはならない（MUST）
```

## 開発手順・回帰確認

L1 の検出挙動は `scan/scan_test.go` に「既知の漏れサンプル集」として固定されている。
モデルを介さず、次の 2 コマンドだけで再現確認できる。

```sh
cd scan
go vet ./...
go test ./...
```

リポジトリルートからは次で同じ内容を実行できる（CI もこのコマンドを使う）。

```sh
go vet ./scan/...
go test ./scan/...
```

### L1 を変更するときの決まり

`design-claude.md` §18 のバージョニング規則に従う。

- **L1（`normalize.go` / `scan.go` / manifest 検証）へ変更を入れる PR は、本スイートが
  グリーンであることを必須とする。** 挙動を意図的に変える場合は、対応するテーブル行の
  期待値の変更が同一 PR の diff に現れるため、レビューで挙動変更が可視化される。
- **新たな取りこぼし（実運用で見つかった漏れサンプル）は、修正 PR で必ず該当テーブルへ
  再現ケースを 1 行追加してから修正する（fail first）。**
- `schema/manifest.schema.json` を変更したときは、`scan/scan.go` の手書き検証と
  `scan/scan_test.go` の `TestManifestValidation` テーブルを同時に更新する。

テーブルの対応関係は [`docs/issue-8-scan-test-design.md`](docs/issue-8-scan-test-design.md) にある。

| テスト | 固定している契約 |
|---|---|
| `TestNormalize` / `TestNormalizeIdempotent` | 正規化パイプライン（NFKC → 小文字化 → カタカナ→ひらがな） |
| `TestScanLiteralLeak` | 表記揺れ（ひらがな/カタカナ/半角カナ/ローマ字/全角）の一致 |
| `TestScanVisibleExceptions` / `TestScanExceptionMasksLiteral` | `visible_exceptions` の回数判定と例外マスク |
| `TestScanNegativeControl` | 偽陽性のない陰性対照、`hits` が `[]`（`null` でない）こと |
| `TestScanHitDetails` / `TestScanHitOrdering` | 行番号・excerpt・hits の整列 |
| `TestManifestValidation` | exit 2 となる manifest 不正の網羅 |
| `TestRunExitCodes` | 終了コードと stdout JSON / stderr カテゴリ |

実装が参照すべき関数シグネチャと「変更不可の契約」3 点は `scan/scan_test.go` 冒頭の
パッケージコメントにまとめてある。
