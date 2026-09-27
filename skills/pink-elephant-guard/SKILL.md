---
name: pink-elephant-guard
description: Prevent rejected, removed, corrected, or forbidden concepts from resurfacing as the topic of a final deliverable. Use after requests such as 「これは消して」「その案はなし」「入れないで」「そこじゃない」 when revising copy, visuals, dialogue, summaries, labels, UI, or other viewer-facing output; do not use when comparison, compliance, safety, or change history explicitly requires naming the excluded item.
---

# Pink Elephant Guard / ピンクの象ガード

却下・削除・訂正・禁止された要素を変更履歴として隔離し、修正後の現在状態だけから完成物を組み直す。
目標は禁止語を伏せることではない。削除跡や言い訳を残さず、現在の目的だけで自然に成立する完成物を返すことである。

本ファイルは L3（メインモデル、つまりあなた）向けの統括手順である。検査は下位層へ委ねる。

| 層 | 実体 | 担当 |
|---|---|---|
| L1 | CLI `pink-elephant-scan`（決定論的な Go バイナリ、トークン 0） | Literal leak: 却下語の字面再侵入、例外語の回数上限 |
| L2 | サブエージェント `pink-elephant-semantic-scan`（Haiku、隔離コンテキスト） | Semantic / Rationale / Attention / Visual leak |
| L3 | 本手順 | 分類、manifest と Clean Brief の作成、生成、再生成、最終判断 |

**PASS 判定は L1 と L2 の結果からのみ導く。自分の読みで代替してはならず、L1/L2 が動いていない初稿を検査済みと報告してはならない。**

## 適用する場面

次の 3 条件をすべて満たすとき:

1. 会話内に、却下・削除・訂正・禁止された要素がある。
2. ユーザーが、鑑賞者または利用者向けの完成物（コピー、見出し、CTA、台詞、ナレーション、字幕、画像・動画生成プロンプト、画像内文字、UI 文言、ラベル、配布用の要約）を新規作成または修正しようとしている。
3. 完成物で、その履歴を説明する必要がない。

発動フレーズの例（文字列一致ではなく依頼全体の意味で判断する）:
「これは消して」「その案はなし」「入れないで」「そこじゃない」「企画から外れました」「前の設定は使いません」

## 適用しない場面

次の場合は除外対象を隠さず、本スキルを適用しない:

- A 案と B 案の比較、変更履歴、議事録、監査記録
- 法令、契約、広告開示、安全、医療、アレルゲン、リスク説明、アクセシビリティ表示
- 障害分析、原因調査、再発防止
- ユーザーが明示的に必要とした不使用訴求（その語は例外として保持する）
- 訂正履歴が存在しない通常の新規制作

本スキルは、事実、失敗、危険、義務的な開示を見栄えのために隠す仕組みではない。

## 優先順位

判断が競合するときは次の順で優先する:

1. 法令、安全、契約、アクセシビリティ上の必要表示
2. ユーザーが完成面へ明示するよう指定した内容
3. ユーザーが最後に確定した現在状態
4. それ以前の案や変更履歴

## 処理工程

流れ: `1 → 2 → 3 → 4a → 4b →（FAIL なら 5 → 4a へ）→ 最終判断 → 出力`
中間ファイルはセッションのスクラッチパッドへ置き、ユーザーのリポジトリへは置かない。

### 工程1: 分類して manifest を書く

会話を次の 4 つに分類する:

- `TARGET_STATE`: 修正後に存在し、伝えるべき現在状態
- `REJECTED_HISTORY`: 取り下げた案、削除対象、誤り、修正理由
- `VISIBLE_EXCEPTIONS`: 完成面へ残す必要がある比較、開示、不使用訴求
- `SURFACE`: 受け手が見る見出し、本文、CTA、台詞、画像、UI、ナレーション等

次に `pink-elephant-manifest.json` をスクラッチパッドへ書く。機械可読なスキーマはリポジトリルートの
`schema/manifest.schema.json` で、違反があれば L1 が差し戻す。

| フィールド | 書き方 |
|---|---|
| `schema_version` | `1` |
| `target_state` | 現在状態の要約（Clean Brief の骨子）。会話の転記ではない |
| `rejected[].id` / `label` | 一意な id（`^[a-z0-9][a-z0-9_-]{0,31}$`）と人間向けの表示名 |
| `rejected[].literal_terms` | 再侵入しうるすべての表記。**表記揺れは自分で展開する（SHOULD）**: ひらがな / カタカナ / ローマ字 / 英語 / 略称 / 会話中で使われた通称。L1 は正規化（NFKC・大小文字・かな相互）だけを行い、展開はしない。L1 の検出力はこの列挙に依存する |
| `rejected[].concept` | その要素が**何であるか**。言い換えを認識できるように書く。却下理由は書かない |
| `visible_exceptions[]` | `term`、`max_occurrences`、任意で `allowed_surfaces`（`surface` の値）と `reason`。ここに置いた語を `literal_terms` にも入れてはならない |
| `surface` | 受け手が見る面のラベル。例: `["headline", "body", "cta"]`。**必ず宣言する**: L2 の Attention 検査はこれに依存する。生成プロンプトでは画像・動画系のラベル（`image_prompt`、`video_prompt`、`storyboard`）を使う |

manifest に会話全文、顧客情報、秘密、ローカルのフルパスを入れない。

不在自体を訴求するかどうかで完成物が大きく変わり、会話から判断できない場合だけ質問する。
それ以外は現在状態を優先して進める。

### 工程2: Clean Brief を作る

`TARGET_STATE`、許可された事実、媒体、トーン、必要な構造だけで内部用 brief を組み直す。

MUST:

- 「X を出さない」ではなく、X がなくても成立する肯定形で書く。
- 削除後の空白を、目的に合う内容、構図、動作、情報階層で埋める。
- 却下理由や元案を、生成用 brief へ再投入しない。

### 工程3: 現在状態から初稿を生成する

Clean Brief だけを制作上の正本とする。修正前の文章から数語を削る方式ではなく、現在状態を主語にして最初から組み直す。

初稿はスクラッチパッドのファイル（例: `pink-elephant-draft.txt`）へ書き出す。
**各面の先頭行に `<surface の値>:` の形のラベルを付け、値は `surface` に宣言したものと完全に一致させる。**
L2 は面を推定しない。ラベルが無い、または一致しない初稿では Attention 検査が実施されない。

```text
headline: 今月はドリップバッグの詰め合わせ
body: 常温で持ち運べる焙煎違いの3種をそろえました。
cta: 店頭でお受け取りください
```

### 工程4a: L1 字面検査（fail-fast）

```bash
pink-elephant-scan --manifest <scratchpad>/pink-elephant-manifest.json --draft <scratchpad>/pink-elephant-draft.txt
```

バイナリは `PATH` 上、プラグインとして導入した場合は `${CLAUDE_PLUGIN_ROOT}/bin/pink-elephant-scan`、
または `scan/` からビルドしたもの（リポジトリの README 参照）。分岐は終了コードだけで行う:

| exit | 意味 | 対処 |
|---|---|---|
| 0 | PASS | 工程 4b へ |
| 1 | FAIL。`stdout` に `hits[]`（`term`、`line`、`excerpt`） | 工程 5 へ。**L2 は呼ばない** |
| 2 | 呼び出し方の誤り | コマンドを直す |
| 3 | manifest 不正（詳細は `stderr`） | 工程 1 へ戻り manifest を作り直す |
| 4 | 初稿を読めない | 初稿ファイルを書き直して再実行 |
| 5 またはその他 | 判定なし | 未検査として扱う。繰り返すなら報告する |

L1 をまったく実行できない場合（バイナリも Go も無い）、代わりに自分で初稿を検査してはならない。
機械検査が実施できなかったことを明記して出す。

### 工程4b: L2 意味検査（隔離サブエージェント）

Agent ツールで `pink-elephant-semantic-scan` を起動する。**渡すのは 2 つのファイルパスだけ。
会話履歴、却下理由、以前の稿、自分による削除内容の要約を含めてはならない（MUST）。**
会話から隔離されていることが、汚染されない判定の根拠である。

```text
manifest: <scratchpad>/pink-elephant-manifest.json
draft: <scratchpad>/pink-elephant-draft.txt
判定の JSON だけを返してください。
```

返ってきた JSON（スキーマ: `schema/semantic-scan-output.schema.json`）を読む:

- `pass: true`: 機械検査は完了。最終判断へ。
- `pass: false`: `findings[]`（`check`、`line`、`quote`、`note`、`rejected_id` または `exception_term`）を持って工程 5 へ。
- `pass` が無い（`{"error": ...}`）: 判定なし。`draft_unreadable` なら初稿を書き直して 4a → 4b を再実行。
  `manifest_*` なら工程 1 へ。
- 面ラベルを付けたのに `applied_checks` に `"attention"` が無い: ラベルが `surface` と一致していない。
  初稿か manifest を直して再実行。画像・動画プロンプトなのに `"visual"` が無い: `surface` に画像・動画系の面を宣言して再実行。

findings に修正案は含まれず、L2 に求めてもならない。再生成は L3 の責務である。

### 工程5: Clean Brief から再生成する

4a または 4b が FAIL のとき、指摘された語だけを消して再提出してはならない。
説明構造や構図自体がまだ旧案中心である可能性が高い。findings を手掛かりに Clean Brief を点検し、
必要なら brief を調整してから、初稿全体を brief から生成し直し（工程 3）、4a → 4b を再実行する。

**2 回連続で FAIL したら、誤っているのは言い回しではなく分類である。** 工程 1 へ戻り manifest を作り直す:
`literal_terms` の表記揺れ不足、`concept` が狭すぎるか広すぎる、例外に置くべき語が `rejected` にある（またはその逆）。
1 サイクルは 4a/4b を通って FAIL で終わる 1 回分を指し、判定なし（`error`）は数えない。

### 最終判断（L3）

L1 と L2 の両方が PASS したあとにのみ行う。完成物が現在案だけで自然に成立しているか（検収基準の最終項目）を確認する。
たとえば全文が「〜はありません」調なら、Clean Brief を調整して 4a → 4b をもう一度通す。
この判断は PASS した稿を差し戻すことはできるが、未検査や FAIL の稿を通すことはできない。

検収基準と、それを確立する層:

| 基準 | 確立する層 |
|---|---|
| `VISIBLE_EXCEPTIONS` にない却下要素の語句が 0 件 | L1（exit 0） |
| 同義表現、否定形、婉曲表現による意味残留が 0 件 | L2 `semantic` |
| 削除理由、変更説明、不在の強調が 0 件 | L2 `rationale` |
| タイトル、冒頭、CTA、結論が現在の目的を主題にしている | L2 `attention` |
| 確認可能な範囲に視覚・音声残留がない | L2 `visual`（プロンプト文面のみ） |
| 例外表示が指定された場所、目的、回数を越えていない | L1（回数）、L2 `attention`（配置・意図） |
| 未確認のものを検査済みと報告していない | `applied_checks` と自分の報告 |
| 完成物が削除跡に見えず、現在案だけで自然に成立している | L3（本工程） |

## 媒体別要件

完成物が画像生成プロンプト、動画生成プロンプト、UI 文言・ラベルのときは、工程 2 の前に
同じディレクトリの `references/media-requirements.md` を読む。文章だけの場合はその「文章」節が該当し、
それ以外は不要なので既定では読み込まない。

## 出力

- `TARGET_STATE` だけで自立した完成物を返す。manifest、brief、findings を完成物に含めない。
- ユーザーが「完成稿だけ」と指定した場合、検査報告や工程の説明を添えない。
- 検査報告を求められた場合は、L1 の出力と L2 の JSON を引用する。実施した検査（`applied_checks`）と、
  実画像・実動画・実音声など検査していないものを明記する。自分の読みを検証として書かない。
