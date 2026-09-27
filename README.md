# Pink Elephant Guard

却下・削除・訂正・禁止された要素を変更履歴として隔離し、修正後の現在状態だけから
完成物を組み直すための Claude Code スキル。

仕様は [`design-claude.md`](design-claude.md) が正本。構成は 3 層に分かれる。

| 層 | 実体 | 役割 | 状態 |
|---|---|---|---|
| L1 | `scan/`（Go CLI `pink-elephant-scan`） | 却下語の字面再侵入（Literal leak）を決定論的に検査する。トークン消費 0 | 実装済み |
| L2 | [`agents/semantic-scan.md`](agents/semantic-scan.md)（Haiku サブエージェント） | Semantic / Rationale / Attention / Visual leak を、会話履歴を見ない隔離コンテキストで検査する | 定義済み（実挙動は未検証・#12） |
| L3 | [`skills/pink-elephant-guard/SKILL.md`](skills/pink-elephant-guard/SKILL.md) | 統括・manifest 作成・Clean Brief・生成・再生成・最終判断。工程は 1→2→3→4a（L1）→4b（L2）→5。媒体別要件は [`references/media-requirements.md`](skills/pink-elephant-guard/references/media-requirements.md) へ分離 | 3 層統括版（#10）。実挙動は未検証・#12 |

`pink-elephant-manifest.json` は L1/L2/L3 間の中間生成物で、スキーマの正本は
[`schema/manifest.schema.json`](schema/manifest.schema.json)。利用時に生成される
実ファイルはユーザー内容を含み得るため、コミットしない（`.gitignore` 済み）。

## 動作要件

- Claude Code（スキルとしての利用）
- Go 1.22+ — **L1 CLI のビルド時のみ**必要（[Releases](https://github.com/angiodianxin/pink-elephant-guard/releases) の配布バイナリを使う場合は不要）

## L1 CLI: pink-elephant-scan

以下は `scan/scan_test.go` が固定している外形契約。

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
| 5 | 内部エラー（判定は済んだが stdout へ書けない・予期しない panic） | `internal` | 本 CLI のバグとして報告する |

失敗系（2/3/4/5）では stdout へ検査結果を出さない。exit code は stderr カテゴリと 1:1 に対応し、
機械処理（L3 の分岐・hook・CI）は exit code のみに依存して stderr を解析しない。
呼び出し元は「0/1 以外 = 判定なし」として扱えばよい。

詳細な入出力契約は [`scan/DESIGN.md`](scan/DESIGN.md) を参照。

### ビルド

```sh
cd scan
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../bin/pink-elephant-scan .
```

成果物は `bin/`（`.gitignore` 対象）へ置く。バイナリはコミットせず、
GitHub Releases + SHA-256 チェックサムで配布する。

`scan/go.mod` の外部依存は `golang.org/x/text`（`unicode/norm` の NFKC のみ使用）1 つだけで、
これ以外を追加してはならない（MUST）。版を上げる場合も **必ず版を指定して取得すること。**

```sh
cd scan
go get golang.org/x/text@v0.21.0
```

版なしの `go get golang.org/x/text` は最新版（v0.42.0 時点）を取りに行き、
それが `go >= 1.26` を要求するため `go.mod` の go ディレクティブが 1.22 から自動で引き上げられ、
上記の「Go 1.22+」と両立しなくなる。v0.21.0 は go 1.22 のままで解決できる。

## L2 サブエージェント: pink-elephant-semantic-scan

L1 を PASS した初稿を、[`agents/semantic-scan.md`](agents/semantic-scan.md)（`model: haiku`、`tools: Read`）で
意味レベルまで検査する。**入力は manifest と初稿のパスだけで、会話履歴・却下理由の経緯は渡さない（MUST）。**
検査者自身が旧案に汚染されないための隔離であり、この層の価値そのものである。

prompt ファイルが存在するだけで、実挙動はまだ検証していない（受け入れテストは #12）。
SKILL.md（L3）の工程 4b から Agent ツールで起動する（manifest と初稿のパスのみを渡す）。
L1 のように `go test` で挙動が固定されているわけではない。

| 検査 | 確認内容 | 実施条件 |
|---|---|---|
| Semantic leak | `rejected[].concept` の同義語・上位語・言い換え・否定形・婉曲表現 | 常時 |
| Rationale leak | 却下要素と結びつく削除理由・変更説明（「代わりに」「以前は」等） | 常時 |
| Attention leak | 却下要素の不在・比較が見出し・冒頭・CTA・結論を占めていないか。例外語の配置と意図 | 初稿に面ラベルがある場合 |
| Visual leak | 削除物の輪郭・破片・影・容器・持ち手・プレースホルダー | 画像・動画プロンプトの場合 |

実施した検査は出力の `applied_checks` に列挙する。未実施の検査を含めないことで、
「未確認の媒体を検査済みと報告しない」（検収基準）を出力形式で担保している。
`findings[].check` の値が `applied_checks` に含まれることはスキーマ側で強制している。

### 入力契約: 面ラベル

Attention leak は面ごとの判定を含むため、初稿の各面の先頭行に `<surface の値>:` の形のラベルを付けて渡す
（付与は L3 の責務）。ラベルとして扱われるのは manifest の `surface` に宣言された値と一致するものだけで、
`surface` が無い場合や一致しない場合は面が確定しない。面が確定しない初稿では L2 は面を推定せず、
例外語の配置・意図の検証を含む **Attention leak 全体**を実施しない（`applied_checks` から外れる）。

```text
headline: 今月はドリップバッグの詰め合わせ
body: 常温で持ち運べる焙煎違いの3種をそろえました。
cta: 店頭でお受け取りください
```

### 出力

JSON を 1 個だけ返す。構造の正本は
[`schema/semantic-scan-output.schema.json`](schema/semantic-scan-output.schema.json)。

```json
{
  "pass": false,
  "applied_checks": ["semantic", "rationale", "attention"],
  "findings": [
    {
      "check": "semantic",
      "rejected_id": "r2",
      "line": 2,
      "quote": "夏の冷たい一杯",
      "excerpt": "body: 夏の冷たい一杯のご用意は一区切りとなり、今月は常温で持ち運べるドリップバッグをお届けします。",
      "note": "r2 の concept（夏季限定の冷たい飲み物）の言い換え"
    }
  ]
}
```

- `note` は「なぜ漏れか」だけを書く。**修正文・改善案は返さない**（再生成は L3 の責務）。
- `excerpt` は L1 の `hits[].excerpt` と同じ規則（前後空白を除き 120 文字超は切り詰め）。
- `semantic` / `rationale` の finding は対象の `rejected_id` を必ず持つ。却下要素と紐付かない
  変更告知（現在状態としての「変更となりました」等）は漏れとしない。
- 判定できない場合（ファイルを読めない・末尾まで読み切れない・parse できない）は `pass` を含めず
  `{"error": "..."}` を返す。L1 の「0/1 以外 = 判定なし」と同じく、`pass` の欠如が「判定なし」を表す。
  空の初稿は L1 と揃えて PASS とする（判定不能にしない）。
- `visible_exceptions` の**回数**判定は L1 の責務で、L2 は回数では免除しない。
  L2 は `allowed_surfaces`（配置）と `reason`（意図）の逸脱だけを、列挙された逸脱パターンに限って報告する。

対応する仕様箇所: design-claude.md §7.2（隔離・manifest）、工程4b（4検査）、§12.5（L2/L3 の境界）、
§13.4（サブエージェント定義）、§17（`tools: Read`）。

## 開発手順・回帰確認

L1 の検出挙動は `scan/scan_test.go` に「既知の漏れサンプル集」として固定されている。
モデルを介さず、次の 2 コマンドだけで再現確認できる。

```sh
cd scan
go vet ./...
go test ./...
```

リポジトリルートからは次で同じ内容を実行できる。CI（`.github/workflows/ci.yml`）も同じコマンドを実行する。

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
| `TestInternalErrors` | internal カテゴリ（exit 5）— stdout へ書けない場合に 0/1 を返さないこと、panic を `usage` の exit 2 と衝突させずに写すこと |

実装が参照すべき関数シグネチャと「変更不可の契約」3 点は `scan/scan_test.go` 冒頭の
パッケージコメントにまとめてある。

## CI とリリース

### CI（push / PR で自動実行）

`.github/workflows/ci.yml` が main への push と PR で次を実行する。

| ジョブ | 内容 |
|---|---|
| `go (1.22)` / `go (stable)` | `gofmt -l scan/` が空であること、`go vet ./scan/...`、`go test ./scan/...`、3 プラットフォームのクロスコンパイル（`scripts/build-release.sh`） |
| `version consistency` | `.claude-plugin/plugin.json` の `version` に対応する見出し `## [X.Y.Z]` が `CHANGELOG.md` にあること（`scripts/check-version.sh`） |

Go 1.22 は動作要件の下限（`scan/go.mod`）、stable は最新安定版で、両方で通ることを確認する。

**「失敗するとマージできない」はリポジトリ設定で強制する。** GitHub の Settings → Branches
（またはルールセット）で main を保護し、上記 3 ジョブを required status check に指定する。
ワークフローだけではマージは止まらない。

### リリース手順（`v*` タグ push で自動実行）

バイナリはコミットせず、`.github/workflows/release.yml` が GitHub Releases へ添付する。
`plugin.json` の `version`・Git タグ・`CHANGELOG.md` の見出しは常に一致させる（SemVer、
`design-claude.md` §13.6・§18）。一致は `scripts/check-version.sh` が検査し、不一致ならリリースは作られない。

1. `design-claude.md` §18 に該当する変更があれば `.claude-plugin/plugin.json` の `version` を上げる
2. `CHANGELOG.md` の `## [Unreleased]` の内容を `## [X.Y.Z] - YYYY-MM-DD` へ移す（`Unreleased` のままだとタグ検証で失敗する）
3. ローカルで確認する

   ```sh
   go vet ./scan/... && go test ./scan/...
   scripts/check-version.sh vX.Y.Z
   scripts/build-release.sh          # dist/ に 3 バイナリと SHA256SUMS.txt が出る（.gitignore 対象）
   ```

4. main にマージ後、タグを打って push する

   ```sh
   git tag vX.Y.Z && git push origin vX.Y.Z
   ```

リリースワークフローは タグ検証 → `go vet` / `go test` → `CGO_ENABLED=0` で
linux/amd64・darwin/arm64・windows/amd64 をクロスコンパイル → `SHA256SUMS.txt` 生成 →
CHANGELOG の該当節をリリースノートにして Release を作成、の順に進む。
`v1.2.3-rc.1` のようなプレリリース版はプレリリースとして公開される。

添付物:

```text
pink-elephant-scan_linux_amd64
pink-elephant-scan_darwin_arm64
pink-elephant-scan_windows_amd64.exe
SHA256SUMS.txt        # 同じディレクトリで `sha256sum -c SHA256SUMS.txt` で検証する
```
