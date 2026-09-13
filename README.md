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
- Go 1.22+ — **L1 CLI のビルド時のみ**必要（将来 Releases に配布バイナリを置いた後は、それを使う場合は不要）

## L1 CLI: pink-elephant-scan

> **現在 `scan/` にはテストのみが入っており、実装（`normalize.go` / `scan.go` / `main.go`）は
> 未着手です（[#7](https://github.com/angiodianxin/pink-elephant-guard/issues/7)）。
> そのため `go test ./scan/...` は現時点ではビルドエラーで落ちます。これは TDD の red 段階として
> 意図した状態です。**

以下は `scan/scan_test.go` が固定している、実装が満たすべき外形契約。

```text
usage: pink-elephant-scan --manifest <path> --draft <path>
```

| exit | 意味 | stderr カテゴリ | 呼び出し元の対処 |
|---|---|---|---|
| 0 | PASS（hit 0 件）。stdout に `{"pass":true,"hits":[]}` | — | 次工程（L2）へ進む |
| 1 | FAIL（hit 1 件以上）。stdout に hit の一覧 | — | L3 が初稿を再生成する |
| 2 | 引数不正（未知フラグ・必須欠落・余分な位置引数） | `usage` | 呼び出し方を直す |
| 3 | manifest 不正（読取り不可・パース不能・スキーマ違反・整合性違反） | `manifest` | L3 が manifest を作り直す |
| 4 | draft 読取り不可 | `draft` | draft のパス・生成を直す |

失敗系（2/3/4）では stdout へ何も出さない。exit code は stderr カテゴリと 1:1 に対応し、
機械処理（L3 の分岐・hook・CI）は exit code のみに依存して stderr を解析しない。

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
go get golang.org/x/text@v0.21.0   # unicode/norm のみ使用。これ以外の外部依存を追加してはならない（MUST）
```

**版を必ず指定すること。** 版なしの `go get golang.org/x/text` は最新版（v0.42.0 時点）を取りに行き、
それが `go >= 1.26` を要求するため `go.mod` の go ディレクティブが 1.22 から自動で引き上げられ、
上記の「Go 1.22+」と両立しなくなる。v0.21.0 は go 1.22 のままで解決できることを確認済み。

## 開発手順・回帰確認

L1 の検出挙動は `scan/scan_test.go` に「既知の漏れサンプル集」として固定されている。
モデルを介さず、次の 2 コマンドだけで再現確認できる。

```sh
cd scan
go vet ./...
go test ./...
```

リポジトリルートからは次で同じ内容を実行できる（CI（#1、未実装）にも同じコマンドを使う予定）。

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
| `TestScanVisibleExceptions` / `TestScanExceptionMasksLiteral` | `visible_exceptions` の回数判定（非重複 greedy）と例外マスク（区間管理・超過分も記録・1 rune の重なりで無視・記載順で早い者勝ち） |
| `TestScanNegativeControl` | 偽陽性のない陰性対照、`hits` が `[]`（`null` でない）こと |
| `TestScanHitDetails` / `TestScanHitOrdering` / `TestScanHitTieBreak` | 行番号・excerpt・hits の整列（行 → オフセット → 処理順） |
| `TestManifestValidation` | exit 3 となる manifest 不正の網羅（`scan/DESIGN.md` §5 の全制約） |
| `TestRunExitCodes` | 終了コード（0〜4）、stdout JSON の生文字列（JSON タグと `SetEscapeHTML(false)` を含む）、exit code と stderr カテゴリの 1:1 対応 |

実装が参照すべき関数シグネチャと「変更不可の契約」3 点は `scan/scan_test.go` 冒頭の
パッケージコメントにまとめてある。
