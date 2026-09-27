# Pink Elephant Guard

却下・削除・訂正・禁止された要素を変更履歴として隔離し、修正後の現在状態だけから
完成物を組み直すための Claude Code スキル。

仕様は [`design-claude.md`](design-claude.md) が正本。構成は 3 層に分かれる。

| 層 | 実体 | 役割 | 状態 |
|---|---|---|---|
| L1 | `scan/`（Go CLI `pink-elephant-scan`） | 却下語の字面再侵入（Literal leak）を決定論的に検査する。トークン消費 0 | 実装済み |
| L2 | [`agents/semantic-scan.md`](agents/semantic-scan.md)（Haiku サブエージェント） | Semantic / Rationale / Attention / Visual leak を、会話履歴を見ない隔離コンテキストで検査する | 定義済み（実挙動は未検証・#12） |
| L3 | [`skills/pink-elephant-guard/SKILL.md`](skills/pink-elephant-guard/SKILL.md) | 統括・manifest 作成・Clean Brief・生成・再生成・最終判断。工程は 1→2→3→4a（L1）→4b（L2）→5。媒体別要件は [`references/`](skills/pink-elephant-guard/references/) へ媒体ごとに分離 | 3 層統括版（#10）。実挙動は未検証・#12 |

`pink-elephant-manifest.json` は L1/L2/L3 間の中間生成物で、スキーマの正本は
[`schema/manifest.schema.json`](schema/manifest.schema.json)。利用時に生成される
実ファイルはユーザー内容を含み得るため、コミットしない（`.gitignore` 済み）。

媒体別要件の詳細は [`skills/pink-elephant-guard/references/`](skills/pink-elephant-guard/references/) に
媒体ごとのファイル（`text.md`・`image-prompt.md`・`video-prompt.md`・`ui.md`）として置き、
SKILL.md からは媒体を確定した時点で該当ファイルだけを読む（progressive disclosure、`design-claude.md` §13.1）。

## 動作要件

- Claude Code（スキル・プラグインとしての利用）
- Go 1.22+ — **L1 CLI をソースからビルドする場合のみ**必要（[Releases](https://github.com/angiodianxin/pink-elephant-guard/releases) の配布バイナリを使う場合は不要）

## インストール

スキル（L3）・サブエージェント定義（L2）・L1 バイナリの 3 点が必要になる。
どれか 1 つでも欠けると工程 4a / 4b を実施できず、スキルは「機械検査が実施できなかった」ことを明記して出力する。

導入方法は 2 つある。通常は **プラグインとして導入する**（3 点がまとめて入り、更新も追従できる）。

### プラグインとして導入する（推奨）

Claude Code で次を実行する。

```text
/plugin marketplace add angiodianxin/pink-elephant-guard
/plugin install pink-elephant-guard@pink-elephant-guard
```

ターミナルからは同じことを次で行える。

```sh
claude plugin marketplace add angiodianxin/pink-elephant-guard
claude plugin install pink-elephant-guard@pink-elephant-guard
```

導入後、**新しいセッション**を開始すると読み込まれる。プラグイン経由の名前は次のとおり（プラグイン名で名前空間が付く）。

| 種類 | 名前 |
|---|---|
| スキル | `/pink-elephant-guard:pink-elephant-guard`（「これは消して」「その案はなし」のような依頼では自動で発動する） |
| サブエージェント | `pink-elephant-guard:pink-elephant-semantic-scan` |

#### L1 バイナリの自動準備

プラグインの `bin/pink-elephant-scan` は実体ではなくランチャー（シェルスクリプト）で、プラグインが有効な間は
Bash ツールの `PATH` に載る。バイナリはコミットしない方針のため、**初回実行時に**次の順で実体を用意してキャッシュする。

1. `go`（1.22+）があれば、プラグインに同梱の `scan/` からビルドする
2. ビルドできなければ（Go が無い・版が古い・ビルド失敗）、GitHub Releases から同じ版のバイナリを取得し、
   `SHA256SUMS.txt` で SHA-256 を検証する。一致しなければ使わない

キャッシュ先は `~/.cache/pink-elephant-guard/v<version>/`（`XDG_CACHE_HOME`、または環境変数
`PINK_ELEPHANT_SCAN_CACHE` で変更できる）。2 回目以降はキャッシュを直接実行し、ネットワークにも Go にも触れない。
準備中の表示はすべて stderr に出し、stdout は検査結果の JSON だけに保つ。実体を用意できなかった場合は
exit 127 で終了する（「0/1 以外 = 判定なし」として扱われる）。

必要なもの:

- `sh` と基本コマンド（Windows では Claude Code が使う Git Bash で動く）
- ビルドする場合: Go 1.22+（初回のみ `golang.org/x/text` の取得にネットワークが要る）
- ダウンロードする場合: `curl` または `wget`、`sha256sum` または `shasum`。配布バイナリがあるのは
  linux/amd64・darwin/arm64・windows/amd64 だけで、それ以外（Intel Mac、arm64 Linux 等）は Go が必要

> claude.ai と Cowork は、トップレベルに `bin/` を持つプラグインを導入しない。本プラグインは Claude Code での利用を前提とする。

#### アップデート・アンインストール

```sh
claude plugin marketplace update pink-elephant-guard
claude plugin update pink-elephant-guard@pink-elephant-guard
```

```sh
claude plugin uninstall pink-elephant-guard@pink-elephant-guard
rm -rf ~/.cache/pink-elephant-guard   # L1 バイナリのキャッシュ（版ごとに残るので、古い版だけ消してもよい）
```

変更内容は [`CHANGELOG.md`](CHANGELOG.md) を参照。

### 手動で配置する

プラグイン機能を使わない場合は 3 点を自分で配置する。スキルとサブエージェントには名前空間が付かない
（`/pink-elephant-guard`、`pink-elephant-semantic-scan`）。

#### 1. リポジトリを取得する

```sh
git clone https://github.com/angiodianxin/pink-elephant-guard.git
cd pink-elephant-guard
git checkout v0.1.0   # リリース版に固定する場合
```

#### 2. スキルとサブエージェント定義を配置する

個人用（全プロジェクト共通）なら `~/.claude/`、特定のリポジトリだけで使うなら `<project>/.claude/` へ置く。

```sh
mkdir -p ~/.claude/skills ~/.claude/agents
cp -r skills/pink-elephant-guard ~/.claude/skills/
cp agents/semantic-scan.md ~/.claude/agents/pink-elephant-semantic-scan.md
```

- スキルはディレクトリごと（`SKILL.md` と `references/`）コピーする。`references/` が欠けると媒体別要件を読めない。
- サブエージェントは frontmatter の `name`（`pink-elephant-semantic-scan`）で呼ばれる。ファイル名は任意だが、揃えておくと管理しやすい。

#### 3. L1 バイナリを配置する

[Releases](https://github.com/angiodianxin/pink-elephant-guard/releases) から自分の環境のバイナリと
`SHA256SUMS.txt` を取得し、チェックサムを検証してから **`pink-elephant-scan`（Windows は `pink-elephant-scan.exe`）に
名前を変えて** `PATH` の通るディレクトリへ置く。スキルは `PATH` 上の `pink-elephant-scan` を呼ぶ。

| 環境 | 添付ファイル |
|---|---|
| Linux (x86_64) | `pink-elephant-scan_linux_amd64` |
| macOS (Apple Silicon) | `pink-elephant-scan_darwin_arm64` |
| Windows (x86_64) | `pink-elephant-scan_windows_amd64.exe` |

Linux / macOS の例（`~/.local/bin` が `PATH` に入っている前提）:

```sh
f=pink-elephant-scan_linux_amd64   # macOS は pink-elephant-scan_darwin_arm64
base=https://github.com/angiodianxin/pink-elephant-guard/releases/download/v0.1.0
curl -fL --remote-name-all "$base/$f" "$base/SHA256SUMS.txt"
sha256sum -c --ignore-missing SHA256SUMS.txt   # macOS は shasum -a 256 -c --ignore-missing SHA256SUMS.txt
mkdir -p ~/.local/bin
install -m 755 "$f" ~/.local/bin/pink-elephant-scan
```

`<ファイル名>: OK` と出れば検証済み。macOS でブラウザからダウンロードした場合は、
Gatekeeper の隔離属性を外す必要がある（`xattr -d com.apple.quarantine ~/.local/bin/pink-elephant-scan`）。

Windows（PowerShell）の例:

```powershell
$f = 'pink-elephant-scan_windows_amd64.exe'
$base = 'https://github.com/angiodianxin/pink-elephant-guard/releases/download/v0.1.0'
Invoke-WebRequest "$base/$f" -OutFile $f -UseBasicParsing
Invoke-WebRequest "$base/SHA256SUMS.txt" -OutFile SHA256SUMS.txt -UseBasicParsing
$expected = ((Select-String -Path SHA256SUMS.txt -SimpleMatch $f).Line -split '\s+')[0]
if ((Get-FileHash $f -Algorithm SHA256).Hash.ToLower() -ne $expected) { throw 'checksum mismatch' }
New-Item -ItemType Directory -Force "$HOME\bin" | Out-Null
Move-Item $f "$HOME\bin\pink-elephant-scan.exe"
```

チェックサムが一致しないと `checksum mismatch` で止まる。`$HOME\bin` が `PATH` に無ければ、
Windows の「環境変数」設定からユーザーの `Path` に追加する。

[GitHub CLI](https://cli.github.com/) があれば、`curl` / `Invoke-WebRequest` の代わりに次でも取得できる。

```sh
gh release download v0.1.0 --repo angiodianxin/pink-elephant-guard -p "$f" -p SHA256SUMS.txt
```

配布バイナリの無い環境（Intel Mac、arm64 Linux 等）や、ソースから入れたい場合は Go 1.22+ でビルドする
（[ビルド](#ビルド)参照）。`dist/pink-elephant-scan` ができるので、同様に `PATH` の通る場所へ置く。

#### 4. 動作を確認する

```sh
pink-elephant-scan --manifest /dev/null --draft /dev/null; echo "exit=$?"
```

`exit=3`（空の manifest を不正として拒否。stderr に `manifest error`）が返れば、バイナリは `PATH` 上で動いている。
`command not found` の場合は `PATH` を見直す。PowerShell では `pink-elephant-scan --manifest NUL --draft NUL; $LASTEXITCODE` で同じ確認ができる。

Claude Code は **新しいセッション**を開始するとスキルとサブエージェントを読み込む。
`/pink-elephant-guard` で明示的に発動でき、「これは消して」「その案はなし」のような依頼では自動で発動する。

#### アップデート・アンインストール

- アップデート: リポジトリで新しいタグを checkout し、手順 2・3 をやり直す（上書きでよい）。
  変更内容は [`CHANGELOG.md`](CHANGELOG.md) を参照。
- アンインストール: 配置した 3 点を削除する。

  ```sh
  rm -r ~/.claude/skills/pink-elephant-guard
  rm ~/.claude/agents/pink-elephant-semantic-scan.md
  rm ~/.local/bin/pink-elephant-scan
  ```

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
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../dist/pink-elephant-scan .
```

成果物は `dist/`（`.gitignore` 対象）へ置く。バイナリはコミットせず、
GitHub Releases + SHA-256 チェックサムで配布する。
`bin/pink-elephant-scan` はプラグイン用のランチャー（コミット対象のシェルスクリプト）なので、ビルド成果物で上書きしない。

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
