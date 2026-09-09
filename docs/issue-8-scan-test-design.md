# 詳細設計書: L1 回帰テストスイート `scan/scan_test.go`（Issue #8）

- 対象 Issue: [#8 L1: 回帰テストスイート（scan_test.go）](https://github.com/angiodianxin/pink-elephant-guard/issues/8)
- 依存 Issue: [#7 L1: pink-elephant-scan CLI の実装（Go）](https://github.com/angiodianxin/pink-elephant-guard/issues/7)
- 参照: design-claude.md §7.2（manifest / exit 2 条件）、§12.2・§12.3（使用例）、§13.3（CLI 仕様・scan_test.go MUST）、§18（バージョニング）、`schema/manifest.schema.json`（スキーマ正本）

## 1. 目的と位置づけ

L1（決定論的CLI）の検出挙動を「既知の漏れサンプル集」としてテストコードに固定化し、
モデルを一切介さず `go test ./scan/...` だけで回帰確認できるようにする。

- L1 の検出力は仕様書が約束する正規化一致（NFKC・小文字化・かな相互）に依存する。
  この約束をテストとして固定することで、L1 変更時（§18）に挙動の後退を機械的に検出する。
- 仕様書の使用例 §12.2（例外の回数判定）・§12.3（Literal leak で止まる例）を
  そのままテストシナリオとして写し取り、仕様と実装の対応を検証可能にする。
- 本テストは L1 のみを対象とする。意味検査（L2）・分類（L3）はスコープ外。

## 2. 前提: テスト対象の内部構成（#7 への要求）

`scan/` は `package main` の単一パッケージであり（§13.1）、`scan_test.go` も同一パッケージに置く。
これにより非公開関数を直接テストでき、バイナリのビルド・exec を必要としない。

テスト容易性のため、#7 の実装は次の分離を満たすこと（本書から #7 への設計上の要求）:

| 関数（想定シグネチャ） | ファイル | 責務 |
|---|---|---|
| `normalize(s string) string` | normalize.go | NFKC → `strings.ToLower` → カタカナ→ひらがな rune シフト（この順） |
| `loadManifest(path string) (*Manifest, error)` | scan.go または main.go | JSON パース、スキーマ検証、整合性制約検証。不正は manifest 不正を示す型付きエラー |
| `scanDraft(m *Manifest, draft string) Result` | scan.go | 照合と `visible_exceptions` の回数判定。`Result{Pass bool, Hits []Hit}` |
| `run(args []string, stdout, stderr io.Writer) int` | main.go | 引数処理と入出力。戻り値がそのまま終了コード（0/1/2）。`main()` は `os.Exit(run(...))` のみ |

実際の関数名・シグネチャが #7 の実装で多少異なるのは許容する。
その場合はテスト側を実装に合わせて調整するが、**次の3点は変更不可の契約**とする:

1. 終了コードのロジックが `main()` から分離され、テストからプロセスを起動せずに検証できること。
2. 正規化・照合・manifest 検証が個別に呼び出せること。
3. 検査結果（pass / hits の term・line・excerpt）が構造体として取得できること。

## 3. テスト方針

- **決定論**: 乱数・時刻・環境変数・ネットワークに依存しない。ゴールデンファイル比較ではなく
  構造体の完全一致で判定する。ただし `Result` は `[]Hit` を含み `==`/`!=` では比較できないため、
  `Result`・hits の比較は `reflect.DeepEqual`（または `slices.Equal` とフィールド単位比較）で行い、
  `got != want` の直接比較は文字列・数値・bool など比較可能な値に限る。
- **テーブル駆動**: 全テストを `[]struct{name, ...}` + `t.Run(tc.name, ...)` のテーブル駆動で書き、
  漏れサンプルの追加が「テーブルへ1行足す」だけで済むようにする。
- **フィクスチャはインライン**: manifest・初稿とも Go の raw string literal としてテストファイル内に置き、
  「既知の漏れサンプル集」が `scan_test.go` 単体で完結するようにする（`testdata/` ディレクトリは作らない。
  サンプルが肥大化した将来に `testdata/` へ移すのは可）。
- **一時ファイルは `t.TempDir()`**: `run()` の終了コード検証などファイルパスが必要な場合のみ、
  `t.TempDir()` へ書き出して渡す。リポジトリへテスト用の中間ファイルを残さない。
- **陰性対照を必ず持つ**: 検出系のテーブルには「漏れなしで PASS するケース」を併置し、偽陽性の回帰も検出する。
- 題材は仕様書 §13.6 に従い架空のパン屋の例のみを使う。

## 4. 標準フィクスチャ

§12.1〜12.3 を写した標準 manifest をテスト内の定数として定義し、複数のテストで共有する:

~~~json
{
  "schema_version": 1,
  "target_state": "クロワッサン3種と自家焙煎コーヒーの朝フェア告知",
  "rejected": [
    {
      "id": "r1",
      "label": "桜あんぱん",
      "literal_terms": ["桜あんぱん", "さくらあんぱん", "sakura anpan"],
      "concept": "却下された春季限定の菓子パン商品"
    },
    {
      "id": "r2",
      "label": "無料配布",
      "literal_terms": ["無料配布", "むりょうはいふ"],
      "concept": "却下された無償プレゼント施策"
    }
  ],
  "visible_exceptions": [
    { "term": "カフェインレス", "max_occurrences": 1, "allowed_surfaces": ["body"], "reason": "不使用訴求として指定" },
    { "term": "遅めの朝", "max_occurrences": 2 }
  ],
  "surface": ["headline", "body", "cta"]
}
~~~

manifest 不正系のテストでは、この標準 manifest を `map[string]any` で複製して
1箇所だけ壊すヘルパー（`brokenManifest(mutate func(m map[string]any))`）を用意し、
「どの制約を壊したか」がテーブルの各行から読み取れるようにする。

## 5. テストケース詳細設計

### 5.1 `TestNormalize` — 正規化の単体検証

正規化パイプライン（NFKC → 小文字化 → カタカナ→ひらがな）の各段と合成を固定する。

| ID | 入力 | 期待出力 | 検証点 |
|---|---|---|---|
| N-01 | `ｻｸﾗ` | `さくら` | NFKC で半角カナ→全角カナ、その後かなシフト |
| N-02 | `ｻﾞｸﾞﾙﾄ` | `ざぐると` | 半角の濁点合成（NFKC）を経てかなシフト |
| N-03 | `SAKURA ANPAN` | `sakura anpan` | 小文字化 |
| N-04 | `ＳＡＫＵＲＡ` | `sakura` | 全角英字の NFKC + 小文字化 |
| N-05 | `サクラアンパン` | `さくらあんぱん` | カタカナ→ひらがな rune シフト |
| N-06a | `ァ` | `ぁ` | シフト範囲の下端（U+30A1） |
| N-06b | `ヶ` | `ゖ` | シフト範囲の上端（U+30F6） |
| N-06c | `ヴ` | `ゔ` | 範囲内の中間点（U+30F4） |
| N-06d | `ヷ` | `ヷ` | 範囲外・上端+1（U+30F7）は変換しない |
| N-06e | `゠` | `゠` | 範囲外・下端−1（U+30A0）は変換しない |
| N-07 | `コーヒー` | `こーひー` | 長音符 `ー` は変換しない |
| N-08 | `１２３` | `123` | 全角数字の NFKC |
| N-09 | `さくらあんぱん` | `さくらあんぱん` | 冪等（正規化済み入力が変化しない） |

### 5.2 `TestScanLiteralLeak` — 表記揺れの検出（Issue チェックリスト 1〜3）

標準 manifest に対し、初稿1件ごとに期待 hits を固定する。
ローマ字・略称の「展開」は L3 の責務（§7.2）なので、ここで検証するのは
「literal_terms に列挙済みの各表記が、初稿側の表記揺れを正規化で吸収して一致すること」である。

| ID | 初稿（抜粋） | 期待 | 一致する term | 検証点 |
|---|---|---|---|---|
| S-01 | `桜あんぱんの販売は終了しました。` | FAIL, 1 hit (line 1) | `桜あんぱん` | 完全一致（§12.3 の固定シナリオ） |
| S-02 | `さくらあんぱんはもうありません` | FAIL, 1 hit | `さくらあんぱん` | ひらがな表記の一致 |
| S-03 | `サクラアンパンフェア` | FAIL, 1 hit | `さくらあんぱん` | カタカナ→ひらがな相互一致 |
| S-04 | `ｻｸﾗあんぱん復活！` | FAIL, 1 hit | `桜あんぱん` ではなく `さくらあんぱん` に一致 | NFKC（半角カナ）一致（§12.3） |
| S-05 | `SAKURA ANPAN is back` | FAIL, 1 hit | `sakura anpan` | 大文字小文字（§12.3） |
| S-06 | `Sakura Anpan` | FAIL, 1 hit | `sakura anpan` | 混在ケース |
| S-07 | `ＳＡＫＵＲＡ　ＡＮＰＡＮ` | FAIL, 1 hit | `sakura anpan` | 全角英字 + 全角空白の NFKC |
| S-08 | 2行目に `桜あんぱん`、4行目に `無料配布` | FAIL, 2 hits (line 2, line 4) | 各 term | 複数 rejected・複数行、hits の行番号昇順 |
| S-09 | `桜餅とあんぱんを別々に販売` | PASS, 0 hits | — | 部分文字列の合成では一致しない（`桜` と `あんぱん` が離れている）陰性対照 |
| S-10 | `さくらあんぱんセール`（専用 manifest: literal_terms を `["サクラアンパン", "さくらあんぱん"]` の順で列挙） | FAIL, 1 hit | `サクラアンパン` | 正規化後に同形となる複数 term の報告規則（列挙順で最初）の固定 |

補足: 標準 manifest では `桜あんぱん` は漢字を含み正規化で変化しないため、
S-04 の半角カナ入力に一致するのは `さくらあんぱん` のみである。
複数の literal_terms が正規化後に同一形となる場合にどの term として報告するかは実装依存にしない —
**hit の `term` は正規化後に一致した表記のうち literal_terms の列挙順で最初のもの**と契約し、
S-10（専用 manifest。`サクラアンパン` と `さくらあんぱん` はともに `さくらあんぱん` へ正規化される）で
この規則を固定する。

### 5.3 `TestScanVisibleExceptions` — 例外の回数判定（チェックリスト 4）

§12.2 のシナリオを固定する。`allowed_surfaces` は L1 では使用しない（§7.2）ため、
面をまたいだ出現でも回数だけで判定されることを含めて検証する。

| ID | 初稿 | 期待 | 検証点 |
|---|---|---|---|
| V-01 | `カフェインレス` が1回 | PASS, 0 hits | max_occurrences=1 の範囲内は hit に数えない |
| V-02 | `カフェインレス` が2回（1行目と3行目） | FAIL, 1 hit (line 3) | **超過分のみ** FAIL。1回目は hit にならない |
| V-03 | `ｶﾌｪｲﾝﾚｽ` と `カフェインレス` が1回ずつ | FAIL, 1 hit | 出現回数は正規化一致で数える（合計2回 > 1） |
| V-04 | `遅めの朝` が2回 | PASS, 0 hits | max_occurrences=2 の境界値（ちょうど上限） |
| V-05 | `遅めの朝` が3回 | FAIL, 1 hit（3回目の行） | 境界値+1 |
| V-06 | 例外語ゼロ、rejected もゼロ出現 | PASS, 0 hits | 例外は「出現義務」ではない（0回でも PASS） |

### 5.4 `TestScanNegativeControl` — 偽陽性のない陰性対照（チェックリスト 5）

| ID | 初稿 | 期待 |
|---|---|---|
| C-01 | `今週はクロワッサン3種と自家焙煎コーヒーの朝フェア。焼きたてを7時から。`（rejected 語ゼロ・例外語ゼロの正常稿） | PASS, hits は**空配列**（null ではない） |
| C-02 | `visible_exceptions` 省略の manifest + 正常稿 | PASS | 
| C-03 | 空の初稿（0バイト） | PASS, 0 hits |

C-01 では `run()` の stdout JSON もあわせて検証し、`{"pass":true,"hits":[]}` と
シリアライズされること（`hits: null` にならないこと）を固定する。

### 5.5 `TestScanHitDetails` — 行番号・excerpt の契約（チェックリスト 7）

hit の詳細フィールドを次のとおり契約として固定する:

- `line`: 初稿の **1始まり**の行番号。行の区切りは `\n`（`\r\n` は `\r` を行末から除去して扱う）。
- `excerpt`: **正規化前の原文**の該当行全体から改行を除いたもの。v1 では切り詰めを行わない。
- 照合は行単位で行う。literal_terms は改行を含まない前提であり、
  行をまたぐ一致は検出しない（既知の限界として §15 に記載する対象。テストでは
  「行またぎは検出されない」ことを仕様の現状として固定する D-04 を置く）。
- hits の順序: 行番号昇順、同一行内は出現位置（正規化後テキスト上の byte offset）昇順。
- 同一行内の同一 term の複数出現は、出現ごとに1 hit と数える（回数判定と整合させるため）。

| ID | 初稿 | 期待 |
|---|---|---|
| D-01 | 3行の初稿、2行目に `桜あんぱん` | hits[0] = `{term:"桜あんぱん", line:2, excerpt:"（2行目の原文全体）"}` |
| D-02 | `ｻｸﾗあんぱん` を含む行 | excerpt は正規化前の `ｻｸﾗあんぱん` を含む原文行そのまま |
| D-03 | 1行に `桜あんぱん` が2回 | 同一 line の hit が2件、offset 順 |
| D-04 | `桜あん\nぱん`（行またぎ） | PASS（検出しない — 現仕様の限界の固定） |
| D-05 | CRLF 改行の初稿、2行目に一致 | line=2、excerpt に `\r` を含まない |

### 5.6 `TestManifestValidation` — manifest 不正の網羅（チェックリスト 6）

§7.2 の exit 2 条件を1件ずつ壊して検証する。`loadManifest()` が
manifest 不正を示すエラーを返すこと、および `run()` 経由で exit 2 になることを確認する。

| ID | 壊し方 | 対応する §7.2 条件 |
|---|---|---|
| M-01 | JSON として不正（`{` のみ） | パース不能 |
| M-02 | `target_state` 欠落 | 必須欠落 |
| M-03 | `rejected` が空配列 `[]` | 制約違反（minItems 1） |
| M-04 | `rejected[].literal_terms` が空配列 | 制約違反（minItems 1） |
| M-05 | `rejected[].id` が `R1`（大文字） | パターン違反 |
| M-06 | `max_occurrences` が `0` | 制約違反（minimum 1） |
| M-07 | `max_occurrences` が文字列 `"1"` | 型不一致 |
| M-08 | 未知フィールド `"note": "x"` をトップレベルへ追加 | additionalProperties 禁止 |
| M-09 | `schema_version: 2` | 未知の schema_version |
| M-10 | `rejected[].id` が `r1` で重複 | id 重複 |
| M-11 | `visible_exceptions[].term` に `サクラアンパン`（literal_term `さくらあんぱん` と正規化後一致） | term と literal_terms の正規化後重複 |
| M-12 | `allowed_surfaces: ["footer"]`（`surface` に無い値） | allowed_surfaces ⊆ surface 違反 |
| M-13 | `surface` を削除したまま `allowed_surfaces` を残す | surface 宣言の前提違反 |
| M-14 | `literal_terms` に同一文字列の重複 | uniqueItems 違反 |

陰性対照として M-00: 標準 manifest（§4）がそのまま検証を通ること。

### 5.7 `TestRunExitCodes` — 終了コードと stdout JSON

`run()` を `t.TempDir()` 上のファイルで呼び、CLI としての外形契約を固定する。

| ID | 条件 | 期待 exit | 期待 stdout |
|---|---|---|---|
| E-01 | 正常稿（C-01 相当） | 0 | `{"pass":true,"hits":[]}` |
| E-02 | §12.3 の初稿（S-01 相当） | 1 | `pass:false`、hits に term/line/excerpt |
| E-03 | manifest 不正（M-01 相当） | 2 | 検査結果 JSON を出力しない（エラーは stderr） |
| E-04 | `--draft` のファイルが存在しない | #7 で終了コードを確定後に固定（§7 未決事項 1）。確定までスイートへ含めない | 検査結果 JSON を出力しない（先行して契約） |
| E-05 | 引数不足（`--manifest` のみ） | E-04 と同じ決定に従う（§7 未決事項 1）。usage は stderr へ | 検査結果 JSON を出力しない |

## 6. 回帰運用への組み込み（受け入れ条件 3）

- 実行コマンドは `go test ./scan/...` に統一する。README の開発手順、および CI（Issue #1 の
  ワークフロー）から同一コマンドを参照する。CI では `go vet ./scan/...` と併走させる（§13.6）。
- §18 に従い、L1（normalize / scan / manifest 検証）へ変更を入れる PR は本スイートの
  グリーンを必須とする。挙動を意図的に変える場合は、対応するテーブル行の期待値の変更が
  同一 PR の diff に現れるため、レビューで挙動変更が可視化される。
- 新たな取りこぼし（実運用で見つかった漏れサンプル）は、修正 PR で必ず
  該当テーブルへ再現ケースを1行追加してから修正する（fail first）。

## 7. 未決事項（#7 実装時に確定し、本書へ反映する）

1. **入出力エラーの終了コード**: draft/manifest ファイルが開けない・引数不足の場合の exit 値。
   §13.3 は 0/1/2 のみ定義しており、0（PASS）と 1（FAIL）は検査結果の意味を持つため流用できない。
   次のいずれかを #7 で確定し、§13.3 へ反映する:
   - 案 (a): manifest が開けない場合は「manifest 不正」に含めて exit 2 とし、
     draft が開けない・引数不足は新コード（例: exit 3 = usage/IO エラー）を §13.3 に追記する。
   - 案 (b): 入出力・usage エラーもすべて exit 2 に寄せる（2 の意味を「検査を実施できなかった」へ広げる）。
   確定後に E-04 / E-05 の期待値をテストとして固定する。それまで両ケースは
   「検査結果 JSON を stdout に出力しない」ことのみ検証する。
2. **スキーマ検証の実装方式**: `schema/manifest.schema.json` を実行時に読むか、
   Go コードへ検証ロジックを写すか。§13.3 の「外部依存は `golang.org/x/text` のみ」の制約から
   JSON Schema バリデータは導入できないため、Go 実装が正本スキーマと同値であることを
   本スイート（5.6）が事実上担保する。スキーマ変更時は 5.6 のテーブル更新を必須とする。
3. **関数シグネチャ**: §2 の想定シグネチャは #7 の実装で確定した名前に本書を追従させる。
