# pink-elephant-scan 詳細設計書

対象: Issue [#7](https://github.com/angiodianxin/pink-elephant-guard/issues/7)（L1: pink-elephant-scan CLI の実装（Go））
上位仕様: `design-claude.md` §7.1（層の責務）・§7.2（manifest）・§13.3（CLI 仕様）・§12.2/§12.3（受け入れ例）
manifest の正本: `schema/manifest.schema.json`（JSON Schema draft 2020-12）

本書は上記仕様を実装可能な粒度まで落とした詳細設計であり、仕様と矛盾した場合は上位仕様を優先する。

---

## 1. 目的とスコープ

L1（決定論的層）の CLI `pink-elephant-scan` は、初稿ファイルと manifest を入力に
**Literal leak（却下語の字面再侵入）** を検査する。トークン消費 0・再現性 100% で、
L2（Haiku サブエージェント）を呼ぶ前に fail-fast させる。

スコープ内:

- manifest の構文・スキーマ・整合性検証（違反は exit 3）
- `rejected[].literal_terms` の正規化一致検出
- `visible_exceptions[].max_occurrences` による回数免除と超過検出
- 結果の JSON 出力と終了コードによる判定伝達

スコープ外（他層の責務）:

- 表記揺れの展開（L3 が manifest 作成時に済ませる。L1 は正規化のみ）
- 形態素解析・意味検査・同義語判定（L2 の責務）
- `allowed_surfaces` / `surface` / `reason` / `concept` を使った判定（L2/L3 の参考情報。L1 は検証のみ行い判定には使わない）
- 初稿の修正・再生成（L3 の責務）

## 2. CLI インターフェース

### 2.1 呼び出し

```text
usage: pink-elephant-scan --manifest <path> --draft <path>
```

| フラグ | 必須 | 内容 |
|---|---|---|
| `--manifest <path>` | MUST | `pink-elephant-manifest.json` のパス |
| `--draft <path>` | MUST | 検査対象の初稿ファイル（UTF-8 テキスト）のパス |

- 上記以外のフラグ・位置引数は受け付けない。未知フラグ・必須欠落は usage を stderr へ出して exit 2。
- 実装は標準ライブラリ `flag` を **`flag.ContinueOnError`** で用いる。`flag.ExitOnError` は解析エラー時に `os.Exit(2)` を直接呼び独自書式のエラー文を出すため、§2.4 の stderr 書式契約を満たせない。解析エラーは呼び出し元が §2.4 の書式（category = `usage`）へ整形し、exit 2 で終了する。`FlagSet.SetOutput(io.Discard)` で `flag` 自身の出力は抑止する。

### 2.2 終了コード

| exit | 意味 | stderr カテゴリ（§2.4） | 呼び出し元の対処 |
|---|---|---|---|
| 0 | PASS（hit 0 件） | — | 次工程（L2）へ進む |
| 1 | FAIL（hit 1 件以上） | — | L3 が初稿を再生成する |
| 2 | 引数不正（未知フラグ・必須欠落・余分な位置引数） | `usage` | 呼び出し方を直す（L3/hook/CI の組込みバグ） |
| 3 | manifest 不正（読取り不可・パース不能・スキーマ違反・整合性違反） | `manifest` | L3 が manifest を作り直す |
| 4 | draft 読取り不可 | `draft` | draft のパス・生成を直す |
| 5 | 内部エラー（判定は済んだが stdout へ書けない・予期しない panic） | `internal` | 本 CLI のバグとして報告する |

- 0/1 は「検査の判定」専用とする。検査が実施できなかった失敗、および判定を伝達できなかった失敗は必ず 2/3/4/5 のいずれかで返し、0/1 を返してはならない。
- 失敗系の exit code は stderr カテゴリと 1:1 に対応する。機械処理（L3 の分岐・hook・CI）は exit code のみに依存し、stderr を解析しない。
- **予期しない失敗は `main()` で `recover()` し、exit 5 へ写す（MUST）。** Go ランタイムは回復しない panic を **exit 2** で終了させるため、素通しにすると引数不正（`usage`）と区別できず、呼び出し元が「呼び出し方が間違っている」と誤解する。判定は済んだが stdout へ書き出せなかった場合も同じく exit 5 とする。
- 以上により「0/1 以外 = 判定なし」が成立し、呼び出し元はそれだけを見て分岐できる。ただし SIGPIPE 等でプロセスがシグナル終了する場合（シェル上は exit 128+N）は本 CLI の制御外で、これも 0/1 以外として扱われる。

### 2.3 標準出力（exit 0 / 1 のとき）

1 行の JSON を stdout へ出力する。

```json
{"pass": true, "hits": []}
```

```json
{"pass": false, "hits": [{"term": "桜あんぱん", "line": 1, "excerpt": "桜あんぱんの販売は終了しました。今週は…"}]}
```

| フィールド | 型 | 内容 |
|---|---|---|
| `pass` | bool | hit 0 件なら true |
| `hits` | array | hit の一覧。0 件でも `[]` を出す（`null` にしない） |
| `hits[].term` | string | 一致した manifest 上の語（**正規化前の原表記**。`literal_terms` の要素、または超過した `visible_exceptions[].term`） |
| `hits[].line` | int | 初稿内の行番号（1 始まり） |
| `hits[].excerpt` | string | 該当行の原文。前後の空白を除去し、120 rune を超える場合は先頭 120 rune + `…` に切り詰める |

- excerpt の「前後の空白」は Unicode の White_Space（`strings.TrimSpace` 相当）とする。半角スペース・タブのほか全角スペース `　`(U+3000) や行末に残った `\r` も除去対象。
- `hits` は行番号昇順 → 行内の出現位置（正規化後テキスト上の rune オフセット）昇順で整列する。
- 同一行に同一語が複数回出現した場合、出現ごとに 1 hit とする。
- 出力は `encoding/json` で生成し、`SetEscapeHTML(false)` で日本語をそのまま出す。

### 2.4 標準エラー出力（exit 2 / 3 / 4 / 5 のとき）

stdout には何も出力せず、stderr へ 1 行のメッセージを出す。

```text
pink-elephant-scan: manifest error: rejected[1].id "r1" duplicates rejected[0].id
pink-elephant-scan: draft error: open draft.txt: no such file or directory
```

書式は `pink-elephant-scan: <category> error: <詳細>`（category = `usage` / `manifest` / `draft` / `internal`）。
category は §2.2 の exit code と 1:1 に対応する（usage=2 / manifest=3 / draft=4 / internal=5）。
`internal` のみ、診断のため 1 行目に続けてスタックトレースを出してよい（他のカテゴリは 1 行のみ）。
機械処理は終了コードのみに依存させ、stderr の文面は人間向けとする（category 接頭辞を含め文面は後方互換の対象にしない — 区別が必要な機械処理は exit code を使う）。

## 3. ファイル構成と責務

```text
scan/
├─ go.mod         # module github.com/angiodianxin/pink-elephant-guard/scan / go 1.22 / require golang.org/x/text
├─ main.go        # フラグ処理、ファイル読込、manifest検証の呼出し、JSON出力、終了コード決定
├─ normalize.go   # Normalize(string) string — NFKC・小文字化・カタカナ→ひらがな
├─ scan.go        # manifest型定義と検証、照合ロジック（Scan(manifest, draft) Result）
└─ scan_test.go   # 回帰テスト（Issue #8。本書 §8 の観点表が仕様）
```

- `main.go` は入出力と終了コードのみを担い、判定ロジックを持たない（テストは `normalize.go` / `scan.go` の公開関数に対して書く）。
- 引数処理〜終了コード決定は `run(args []string, stdout, stderr io.Writer) int` に分離し、`main()` は `os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))` のみとする。CLI の外形契約（exit / stdout JSON / stderr）はプロセスを起動せず `run` に対してテストする（`docs/issue-8-scan-test-design.md` §2 の契約）。
- パッケージは `main` 1 つとし、`Normalize` / `ParseManifest` / `Scan`（および `run`）をパッケージ内の関数として `scan_test.go` から直接叩く。

### 3.1 依存

- Go 1.22+、標準ライブラリ、および `golang.org/x/text`（`unicode/norm` のみ使用）。**これ以外の外部依存を追加してはならない（MUST）。**
- JSON Schema 検証ライブラリは導入しない。`schema/manifest.schema.json` が定める制約は Go コードへ手書きで写す（§5）。スキーマファイルとの二重管理になるため、スキーマ変更時は `scan.go` の検証と `scan_test.go` を同時に更新する（§18 バージョニング）。

## 4. 正規化仕様（normalize.go）

照合は「正規化後テキスト同士の substring 一致」で行う。`literal_terms`・`visible_exceptions[].term`・初稿の各行の全てに同一の関数を適用する。

```go
func Normalize(s string) string
```

処理は次の 3 段を **この順で** 適用する（MUST）。

1. **NFKC 正規化**: `norm.NFKC.String(s)`
   - 半角カナ→全角カナ（`ｻｸﾗ`→`サクラ`、濁点・半濁点は合成される）
   - 全角英数→半角英数（`ＳＡＫＵＲＡ`→`SAKURA`、`１`→`1`）
   - 全角スペース→半角スペース、㌕ 等の互換文字の展開
2. **小文字化**: `strings.ToLower(s)`（`SAKURA`→`sakura`）
3. **カタカナ→ひらがな rune シフト**: U+30A1（ァ）〜U+30F6（ヶ）の各 rune から `0x60` を引き、U+3041（ぁ）〜U+3096（ゖ）へ写す。`ヴ`(U+30F4)→`ゔ`(U+3094)、`ヵ`→`ゕ`、`ヶ`→`ゖ` を含む。

段 3 の対象外として原表記のまま残すもの:

- 長音記号 `ー`(U+30FC)、中点 `・`(U+30FB)、繰返し記号 `ヽヾ`(U+30FD/30FE)
- ひらがな対応の無い `ヷヸヹヺ`(U+30F7〜U+30FA)
- カタカナフレーズ拡張・小書き拡張などの上記範囲外の rune

設計上の含意:

- `カフェインレス` と `かふぇいんれす`、`ｻｸﾗあんぱん` と `さくらあんぱん`、`SAKURA ANPAN` と `sakura anpan` が、それぞれ同一の正規化結果になる。
- **漢字⇔かな（`桜`⇔`さくら`）は正規化で吸収しない。** これは表記揺れ展開であり、L3 が `literal_terms` に列挙する責務（§7.2）。§12.3 の受け入れ例が成立するのは、manifest 側に `"桜あんぱん", "さくらあんぱん", "sakura anpan"` が展開済みだからである。
- 長音・中点・空白の除去や揺らぎ吸収（`サーバ`/`サーバー` 等）は行わない。必要なら L3 が展開する。
- 形態素解析・分かち書きは行わない（MUST NOT）。部分文字列一致のため、語境界は考慮しない（`りんご` は `りんごあめ` にも hit する。過剰検出は再生成コストで許容し、取りこぼしを許容しない方針）。
- 照合は行単位（§6.1）のため、`literal_terms`・`visible_exceptions[].term` は改行を含まない単一行の語とする。改行（CR/LF）を含む語は manifest 不正（exit 3、§5）として拒否する — 受理すると、どの行にも一致し得ない語が有効扱いとなり、検出漏れが黙って通るため。初稿側で行をまたぐ出現は検出しない（既知の限界。テスト設計 D-04 で現状仕様として固定する）。

正規化により文字数・オフセットが原文とずれるため、**行番号は原文の行分割で確定し、行内の照合と出現位置は正規化後の行テキスト上で行う**。excerpt は原文の行から §2.3 の規則（前後空白除去・120 rune 超は切り詰め）で生成し、正規化後オフセットから原文オフセットへの逆写像は実装しない（設計判断: 決定論と単純さを優先）。

## 5. manifest 検証仕様（exit 3 の条件）

`ParseManifest([]byte) (*Manifest, error)` が次を順に検証し、最初の違反で error を返す（main が exit 3 に写す）。
manifest ファイル自体の読取り不可も同じ `manifest` カテゴリの失敗として exit 3 で返す（§2.2）。

1. **JSON パース**: `json.Decoder` + `DisallowUnknownFields()` でデコードする。パース不能・未知フィールドは不正（additionalProperties 禁止に対応）。デコード成功後にもう一度 `Decode` を呼び `io.EOF` を確認することで、単一の JSON 値の後に別の値やゴミが続く入力（例: `{...}{...}`）を不正として弾く。
2. **スキーマ制約**（`schema/manifest.schema.json` と同値になるよう手書き検証）:
   - `schema_version`: 必須、`1` 以外は不正（未知の版の差し戻し）
   - `target_state`: 必須、1〜2000 文字（rune 数）
   - `rejected`: 必須、1 件以上
   - `rejected[].id`: 必須、`^[a-z0-9][a-z0-9_-]{0,31}$`
   - `rejected[].label`: 必須、1〜200 文字
   - `rejected[].literal_terms`: 必須、1 件以上、各要素 1〜200 文字・改行（CR/LF）を含まない、**原表記での重複なし**
   - `rejected[].concept`: 必須、1〜500 文字
   - `visible_exceptions[].term`: 必須、1〜200 文字・改行（CR/LF）を含まない
   - `visible_exceptions[].max_occurrences`: 必須、1 以上の整数
   - `visible_exceptions[].allowed_surfaces`: 任意、あれば 1 件以上・重複なし・各要素 1〜50 文字
   - `visible_exceptions[].reason`: 任意、あれば 1〜300 文字
   - `surface`: 任意、重複なし、各要素 1〜50 文字
3. **整合性制約**（§7.2）:
   - `rejected[].id` が manifest 全体で一意
   - `visible_exceptions[].term` は、いずれの `literal_terms` とも **正規化後に** 一致しない
   - `allowed_surfaces` の各値が `surface` に含まれる（`allowed_surfaces` 使用時は `surface` の宣言が前提）

補足:

- 文字数制約は rune 数で数える（スキーマの minLength/maxLength はコードポイント単位）。
- JSON の数値は `json.Number` で受け、`max_occurrences` が整数であること（`1.5` は不正）を確認する。
- **明示的な `null` は欠落と区別して不正とする。** JSON Schema では任意フィールドも、存在する場合は宣言された型でなければならず `null` は不正。Go の slice/pointer へ直接デコードすると `null` と欠落が同じ nil になるため、任意フィールド（`visible_exceptions`、`surface`、`allowed_surfaces`、`reason` 等）は `json.RawMessage` などで存在有無を追跡し、存在して値が `null` の場合は不正とする（必須フィールドの `null` は型不一致として同様に不正）。
- `schema_version` フィールド自体の欠落と、値が `1` 以外の場合はどちらも不正だが、エラーメッセージは区別する（欠落 / unsupported version）。

## 6. 照合仕様（scan.go）

### 6.1 前処理

1. draft ファイルを全読込する（想定サイズは数 KB〜数 MB の原稿。ストリーミングはしない）。
   - 不正 UTF-8 は不正入力とせず、`norm` の挙動に従い U+FFFD 置換のまま照合する（バイナリ検査は用途外）。
2. `\n` で行分割し、各行末尾の `\r` を除去する（CRLF 対応）。行番号は 1 始まり。
3. 各行に `Normalize` を適用し、正規化済み行の配列を得る。
4. manifest 側も同様に、全 `literal_terms` と全 `visible_exceptions[].term` を正規化する（原表記は hit 報告用に保持する）。

### 6.2 手順1: visible_exceptions の回数判定とマスク

`literal_terms` より先に例外語を処理する。例外語と禁止語が正規化後に「完全一致」することは exit 3 で排除済みだが、**部分文字列関係**（例: `literal_terms` に `カフェ`、例外に `カフェインレス`）はあり得るため、例外語の出現領域をマスクしてから禁止語照合を行う。

1. `visible_exceptions` を manifest 記載順に処理する。各例外語について、初稿全体を行順・行内左→右の順に走査し、正規化済み行内の出現を非重複（`strings.Index` を見つけた長さ分進める greedy）で数える。
2. 各出現について:
   - 通算出現回数が `max_occurrences` 以内 → hit にしない（免除）。
   - 超過分（`max_occurrences + 1` 回目以降） → `{"term": <原表記>, "line": n, "excerpt": <原文行から §2.3 の規則で生成>}` を hit に追加する。
3. 免除・超過を問わず、**出現領域（行番号 + 正規化後行内の rune 区間）をマスク区間リストへ記録する**。テキスト自体は書き換えない。U+FFFD 等の番兵文字への置換は行わない — 番兵文字は draft・manifest の双方に合法的に出現し得るため、「以降の照合対象にならない」保証にならない（例: `literal_terms` に番兵文字そのものが含まれる場合に偽 hit する）。
   - 以降の照合（他の例外語・禁止語）では、出現がマスク区間と 1 rune でも重なる場合、その出現を無視する。
   - 例外語同士が重複し得る場合の挙動は「manifest 記載順に早い者勝ち」で決定論的に定まる。

回数は初稿全体での通算であり、行単位・面（surface）単位ではない。`allowed_surfaces` による配置検査は L2 の責務であり L1 は使わない。

### 6.3 手順2: literal_terms の照合

1. 全 `rejected[].literal_terms` を manifest 記載順（`rejected` の順 → 各 `literal_terms` の順）に処理する。
2. 各語について、正規化済み各行を走査し、マスク区間と重ならない非重複の出現ごとに `{"term": <原表記>, "line": n, "excerpt": <原文行から §2.3 の規則で生成>}` を hit に追加する。
   - 正規化後の形が異なる語同士は、同一領域に重なって hit してよい（`さくら` と `さくらあんぱん` が両方登録されていれば両方 hit する）。マスクは行わない。
   - **正規化後に同形となる複数の語**（例: `サクラアンパン` と `さくらあんぱん`。原表記の重複のみ exit 3 で弾くため、正規化後の重複は合法）は 1 つの照合対象として扱い、1 出現につき hit は 1 件とする。`term` には処理順（`rejected` の順 → 各 `literal_terms` の順）で最初の原表記を報告する。重複排除は同一 `rejected` 内に閉じず manifest 全体で行う（テスト設計 S-10 / S-11 の契約）。
3. 1 出現でも hit があれば FAIL。

### 6.4 結果生成

- hit 全体を「行番号 → 行内出現位置（正規化後 rune オフセット）→ 検出順」で安定ソートする。
- `pass = len(hits) == 0`。exit コードは pass に対応（0/1）。
- hit 件数の上限は設けない（全件報告する。L3 の再生成判断は件数に依存しない）。

### 6.5 計算量

素朴な `strings.Index` 走査で O(行数 × 語数 × 行長)。想定入力（原稿数千行 × 語数十）で数 ms オーダーであり、Aho-Corasick 等の最適化は行わない（依存追加禁止・単純さ優先）。

## 7. ビルドと配布

```text
cd scan
go vet ./...
go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../dist/pink-elephant-scan .
```

- `CGO_ENABLED=0` の単一静的バイナリとしてビルドできること（MUST）。`x/text` は pure Go であり cgo 依存はない。
- クロスコンパイル（SHOULD）:

```text
GOOS=linux   GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../dist/pink-elephant-scan_linux_amd64 .
GOOS=darwin  GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../dist/pink-elephant-scan_darwin_arm64 .
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../dist/pink-elephant-scan_windows_amd64.exe .
```

- `-trimpath` でビルド環境のフルパスを埋め込まない（§17 プライバシー）。
- 成果物は `dist/`（.gitignore 対象）へ置き、配布は GitHub Releases + SHA-256 チェックサム（§13.6）。バイナリはコミットしない（MUST）。
- ネットワーク通信・環境変数・設定ファイルは一切使わない。入力は引数の 2 ファイルのみ、出力は stdout/stderr のみ。

## 8. テスト観点（scan_test.go / Issue #8 への引き継ぎ）

受け入れ条件に対応する回帰ケースを最低限含める。

| # | 観点 | 入力 | 期待 |
|---|---|---|---|
| 1 | 素の一致（§12.3） | terms=`桜あんぱん`、draft「桜あんぱんの販売は終了しました」 | exit 1、hit(term=桜あんぱん, line=1) |
| 2 | 半角カナ（§12.3） | 同上、draft「ｻｸﾗあんぱん」+ terms に `さくらあんぱん` | exit 1（NFKC+かなシフトで一致） |
| 3 | ローマ字大文字/全角（§12.3） | terms=`sakura anpan`、draft「SAKURA ANPAN」「ＳＡＫＵＲＡ ＡＮＰＡＮ」 | exit 1、hit 2 件 |
| 4 | 例外の免除（§12.2） | exception=`カフェインレス`×1、draft に 1 回 | exit 0、hits=[] |
| 5 | 例外の超過（§12.2） | 同上、draft に 2 回 | exit 1、hit は 2 回目の出現のみ |
| 6 | 例外のかな違い | exception=`カフェインレス`×1、draft に「かふぇいんれす」2 回 | exit 1（正規化一致で数える） |
| 7 | 例外と禁止語の部分文字列 | terms=`カフェ`、exception=`カフェインレス`×1、draft「カフェインレス」1 回 | exit 0（マスクにより `カフェ` は hit しない） |
| 8 | PASS | terms=`桜あんぱん`、draft「クロワッサン3種と自家焙煎コーヒー」 | exit 0、`{"pass":true,"hits":[]}` |
| 9 | 複数行・行番号 | 3 行目にのみ一致 | hit の line=3、excerpt=原文行から §2.3 の規則で生成 |
| 10 | manifest: JSON 破損 | `{` | exit 3、stdout 空 |
| 11 | manifest: 未知フィールド | `{"extra": 1, ...}` | exit 3 |
| 12 | manifest: 未知の版 | `"schema_version": 2` | exit 3 |
| 13 | manifest: id 重複 / id パターン違反 | `r1` 重複、`R-1` | exit 3 |
| 14 | manifest: 例外と literal の正規化重複 | terms=`カフェインレス`、exception=`かふぇいんれす` | exit 3 |
| 15 | manifest: allowed_surfaces ⊄ surface | surface 未宣言 + allowed_surfaces あり | exit 3 |
| 16 | draft 不在 | 存在しないパス | exit 4、stderr に draft error |
| 17 | Normalize 単体 | `ｻｸﾗ`→`さくら`、`ＳＡＫＵＲＡ`→`sakura`、`ヴ`→`ゔ`、`ー`→`ー` | 表どおり |
| 18 | manifest: 末尾ゴミ | `{...}{...}`、`{...} x` | exit 3（単一 JSON 値でない） |
| 19 | manifest: 明示的 null | `"visible_exceptions": null`、`"surface": null` | exit 3（欠落は許容、null は不正） |
| 20 | 番兵文字を含む入力 | terms=`�`（U+FFFD）、draft に例外語出現あり | 例外マスクが U+FFFD を偽 hit させない（区間管理の回帰） |
| 21 | manifest: 改行を含む term | literal_terms に `"桜\nあんぱん"` | exit 3 |
| 22 | rejected 横断の重複排除 | r1 に `サクラアンパン`、r2 に `さくらあんぱん`、draft に 1 出現 | exit 1、hit 1 件（term=`サクラアンパン`） |

テストはモデルを介さず `go test ./scan/...` のみで再現でき、CI（および §13.3 の hook/linter 組込み）でそのまま使える。

## 9. 設計判断の記録

| 判断 | 採用 | 理由・棄却案 |
|---|---|---|
| exit code の粒度 | 0/1 = 検査の判定、2 = 引数不正、3 = manifest 不正、4 = draft 読取り不可、5 = 内部エラー | 0/1 は「検査の判定」専用に保つ。失敗系を stderr カテゴリ（usage/manifest/draft/internal）と 1:1 に対応させ、呼び出し元（L3・hook・CI）が「呼び出しを直す / manifest を作り直す / draft のパスを直す / バグとして報告する」を stderr の解析なしに分岐できるようにする。旧契約「入力不正を一律 exit 2 に寄せる（0/1/2 のみ）」は撤廃した。usage=2 は `flag` パッケージ・Unix 慣習（誤用 = 2）とも一致する |
| panic の扱い | `main()` で `recover()` し exit 5 へ写す | Go の既定の panic 終了コードは 2 で、`usage` と衝突する。素通しでは「0/1 以外 = 判定なし」は保てても、呼び出し元が原因を取り違える。exit 5 を internal カテゴリとして立て、非判定系の終了をすべてカテゴリと 1:1 にした。棄却案「表外の値を増やさず既存のどれかへ寄せる」は、引数も manifest も draft も正しいのに usage/manifest/draft を名乗ることになり、カテゴリの意味が壊れる |
| スキーマ検証の実装 | 手書き検証 + `DisallowUnknownFields` | JSON Schema ライブラリは依存追加禁止（MUST）に抵触。二重管理はテストで担保 |
| 例外語と禁止語の部分文字列衝突 | 例外出現領域をマスク区間リストとして記録し、重なる出現を禁止語照合から除外 | 免除したはずの語の内部で禁止語が hit する偽 FAIL を防ぐ。番兵文字への置換は入力に同文字が合法出現し得るため不採用 |
| excerpt の逆写像 | 実装しない（原文行に §2.3 の前後空白除去・切り詰めのみ適用して返す） | 正規化前後のオフセット対応表は複雑さに見合わない。行単位で L3 の再生成判断には十分 |
| 語境界の考慮 | しない（純粋な部分文字列一致） | 形態素解析禁止（MUST NOT）。過剰検出は再生成で吸収し、取りこぼしゼロを優先 |
| マルチパターン照合の最適化 | しない | 想定入力規模で不要。依存ゼロ・可読性優先 |

## 10. 本書が更新を要する変更（§18 連動）

- `schema/manifest.schema.json` の変更（§5 の手書き検証を同時更新）
- CLI の入出力契約（フラグ・exit・stdout JSON）の変更
- 正規化パイプラインの変更（検出結果が変わるため版を上げ、バイナリを再配布）
