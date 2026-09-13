// Package main のテスト。
//
// 本ファイルは L1（決定論的CLI）の回帰テストスイートであり、design-claude.md §13.3 で MUST。
// 設計は docs/issue-8-scan-test-design.md（Issue #8）、対象の詳細設計は scan/DESIGN.md（Issue #7）。
//
// TDD の red 段階として、実装（normalize.go / scan.go / main.go）に先行して置いている。
// 現時点では参照先が未定義のためビルドが通らない。これは意図した状態であり、
// #7 の実装が入った時点で `go test ./scan/...` がグリーンになる。
//
// 実装が満たすべき契約（docs/issue-8-scan-test-design.md §2。本ファイルが参照する識別子）:
//
//	func Normalize(s string) string                        // normalize.go
//	func ParseManifest(b []byte) (*Manifest, error)        // scan.go
//	func Scan(m *Manifest, draft string) Result            // scan.go
//	func run(args []string, stdout, stderr io.Writer) int  // main.go（戻り値がそのまま終了コード。0〜5、scan/DESIGN.md §2.2）
//
//	type Manifest struct{ ... }                            // ParseManifest の戻り値。フィールドは本ファイルからは参照しない
//	type Result struct{ Pass bool; Hits []Hit }            // JSON タグ: pass / hits（hits は 0 件でも [] で出す）
//	type Hit struct{ Term string; Line int; Excerpt string } // JSON タグ: term / line / excerpt
//
// 名前が実装過程で多少変わるのは許容し、その場合は本ファイルを実装に合わせて調整する。
// ただし次の 3 点は変更不可の契約とする:
//
//  1. 終了コードのロジックが main() から分離され、プロセスを起動せず検証できること。
//  2. 正規化・照合・manifest 検証が個別に呼び出せること。
//  3. 検査結果（pass / hits の term・line・excerpt）が構造体として取得できること。
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 失敗系の終了コード（scan/DESIGN.md §2.2・§2.4）
//
// 失敗系の exit code は stderr カテゴリと 1:1 に対応する。機械処理（L3 の分岐・hook・CI）は
// exit code のみに依存し stderr を解析しないため、この対応関係そのものが契約である。
// 対応を 1 箇所で宣言し、各テストはカテゴリを選ぶだけにして、両者が食い違えば落ちるようにする。
// ---------------------------------------------------------------------------

const (
	categoryUsage    = "usage"    // 引数不正（未知フラグ・必須欠落・余分な位置引数）
	categoryManifest = "manifest" // manifest 不正（読取り不可・パース不能・スキーマ違反・整合性違反）
	categoryDraft    = "draft"    // draft 読取り不可
	categoryInternal = "internal" // 内部エラー（判定は済んだが stdout へ書けない・予期しない panic）
)

var failureExit = map[string]int{
	categoryUsage:    2,
	categoryManifest: 3,
	categoryDraft:    4,
	categoryInternal: 5,
}

// checkFailure は失敗系の外形契約を検証する。
// exit code がカテゴリと 1:1 で対応すること、stdout へ検査結果 JSON を出さないこと、
// stderr が §2.4 の書式であること。stderr の詳細文言は後方互換の対象外のため、
// カテゴリ接頭辞までを検証し、それより先には依存させない。
func checkFailure(t *testing.T, category string, code int, stdout, stderr string) {
	t.Helper()

	wantExit, ok := failureExit[category]
	if !ok {
		t.Fatalf("未知の stderr カテゴリ %q", category)
	}
	if code != wantExit {
		t.Errorf("run() = %d, want %d（カテゴリ %s。stderr: %s）", code, wantExit, category, stderr)
	}
	if stdout != "" {
		t.Errorf("exit %d では検査結果 JSON を出力しない, got %q", wantExit, stdout)
	}
	wantPrefix := "pink-elephant-scan: " + category + " error: "
	if !strings.HasPrefix(stderr, wantPrefix) {
		t.Errorf("stderr = %q, want 接頭辞 %q", stderr, wantPrefix)
	}
}

// ---------------------------------------------------------------------------
// 標準フィクスチャ（docs/issue-8-scan-test-design.md §4）
//
// design-claude.md §12.1〜12.3 を写した manifest。複数のテストで共有する。
// 題材は §13.6 に従い架空のパン屋の例のみを使う。
// ---------------------------------------------------------------------------

const standardManifest = `{
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
}`

// cleanDraft は rejected 語・例外語をいずれも含まない正常稿（陰性対照の基準）。
const cleanDraft = `今週はクロワッサン3種と自家焙煎コーヒーの朝フェア。焼きたてを7時から。`

// ---------------------------------------------------------------------------
// ヘルパー
// ---------------------------------------------------------------------------

func mustManifest(t *testing.T, src string) *Manifest {
	t.Helper()
	m, err := ParseManifest([]byte(src))
	if err != nil {
		t.Fatalf("ParseManifest() で予期しないエラー: %v", err)
	}
	return m
}

// brokenManifest は標準 manifest を map として複製し、1 箇所だけ壊して返す。
// 「どの制約を壊したか」がテーブルの各行から読み取れるようにするための道具。
func brokenManifest(t *testing.T, mutate func(m map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(standardManifest), &m); err != nil {
		t.Fatalf("標準 manifest の複製に失敗: %v", err)
	}
	mutate(m)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("複製した manifest の再直列化に失敗: %v", err)
	}
	return b
}

// rejectedAt は複製した manifest の rejected[i] を取り出す。
func rejectedAt(t *testing.T, m map[string]any, i int) map[string]any {
	t.Helper()
	arr, ok := m["rejected"].([]any)
	if !ok || len(arr) <= i {
		t.Fatalf("rejected[%d] を取り出せない", i)
	}
	item, ok := arr[i].(map[string]any)
	if !ok {
		t.Fatalf("rejected[%d] がオブジェクトでない", i)
	}
	return item
}

// exceptionAt は複製した manifest の visible_exceptions[i] を取り出す。
func exceptionAt(t *testing.T, m map[string]any, i int) map[string]any {
	t.Helper()
	arr, ok := m["visible_exceptions"].([]any)
	if !ok || len(arr) <= i {
		t.Fatalf("visible_exceptions[%d] を取り出せない", i)
	}
	item, ok := arr[i].(map[string]any)
	if !ok {
		t.Fatalf("visible_exceptions[%d] がオブジェクトでない", i)
	}
	return item
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("一時ファイルの書き出しに失敗: %v", err)
	}
	return path
}

// wantPass / wantFail は期待 Result を組み立てる糖衣。hits は必ず空配列（null ではない）。
// 実装側（#7）の識別子と衝突しないよう want を冠する。
func wantPass() Result { return Result{Pass: true, Hits: []Hit{}} }

func wantFail(hits ...Hit) Result { return Result{Pass: false, Hits: hits} }

func checkScan(t *testing.T, m *Manifest, draft string, want Result) {
	t.Helper()
	got := Scan(m, draft)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Scan() = %+v\nwant %+v", got, want)
	}
}

// ---------------------------------------------------------------------------
// 5.1 TestNormalize — 正規化の単体検証
// ---------------------------------------------------------------------------

func TestNormalize(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  string
		check string
	}{
		{"N-01 半角カナ", "ｻｸﾗ", "さくら", "NFKC で半角カナ→全角カナ、その後かなシフト"},
		{"N-02 半角の濁点合成", "ｻﾞｸﾞﾙﾄ", "ざぐると", "半角の濁点合成（NFKC）を経てかなシフト"},
		{"N-03 大文字", "SAKURA ANPAN", "sakura anpan", "小文字化"},
		{"N-04 全角英字", "ＳＡＫＵＲＡ", "sakura", "全角英字の NFKC + 小文字化"},
		{"N-05 カタカナ", "サクラアンパン", "さくらあんぱん", "カタカナ→ひらがな rune シフト"},
		{"N-06a シフト範囲の下端", "ァ", "ぁ", "U+30A1"},
		{"N-06b シフト範囲の上端", "ヶ", "ゖ", "U+30F6"},
		{"N-06c 範囲内の中間点", "ヴ", "ゔ", "U+30F4"},
		{"N-06d 範囲外・上端+1", "ヷ", "ヷ", "U+30F7 は変換しない"},
		{"N-06e 範囲外・下端-1", "゠", "゠", "U+30A0 は変換しない"},
		{"N-07 長音符", "コーヒー", "こーひー", "ー は変換しない"},
		{"N-08 全角数字", "１２３", "123", "全角数字の NFKC"},
		{"N-09 冪等", "さくらあんぱん", "さくらあんぱん", "正規化済み入力が変化しない"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Normalize(tc.in); got != tc.want {
				t.Errorf("Normalize(%q) = %q, want %q（%s）", tc.in, got, tc.want, tc.check)
			}
		})
	}
}

// TestNormalizeIdempotent は正規化が冪等であること（二度掛けても変わらないこと）を、
// 上表の全入力について確認する。照合の両辺に同じ関数を適用する前提を守るため。
func TestNormalizeIdempotent(t *testing.T) {
	inputs := []string{
		"ｻｸﾗあんぱん", "ＳＡＫＵＲＡ　ＡＮＰＡＮ", "カフェインレス", "コーヒー", "桜あんぱん", cleanDraft,
	}
	for _, in := range inputs {
		once := Normalize(in)
		if twice := Normalize(once); twice != once {
			t.Errorf("Normalize が冪等でない: %q → %q → %q", in, once, twice)
		}
	}
}

// ---------------------------------------------------------------------------
// 5.2 TestScanLiteralLeak — 表記揺れの検出
// ---------------------------------------------------------------------------

func TestScanLiteralLeak(t *testing.T) {
	std := mustManifest(t, standardManifest)

	// S-10 用: 正規化後に同形となる語を同一 literal_terms 内へ列挙した manifest。
	sameFormOneRejected := mustManifest(t, `{
  "schema_version": 1,
  "target_state": "朝フェア告知",
  "rejected": [
    {
      "id": "r1",
      "label": "桜あんぱん",
      "literal_terms": ["サクラアンパン", "さくらあんぱん"],
      "concept": "却下された春季限定の菓子パン商品"
    }
  ]
}`)

	// S-11 用: 正規化後に同形となる語を別々の rejected へ分けた manifest。
	sameFormTwoRejected := mustManifest(t, `{
  "schema_version": 1,
  "target_state": "朝フェア告知",
  "rejected": [
    {
      "id": "r1",
      "label": "桜あんぱん（カタカナ）",
      "literal_terms": ["サクラアンパン"],
      "concept": "却下された春季限定の菓子パン商品"
    },
    {
      "id": "r2",
      "label": "桜あんぱん（ひらがな）",
      "literal_terms": ["さくらあんぱん"],
      "concept": "却下された春季限定の菓子パン商品の別表記"
    }
  ]
}`)

	tests := []struct {
		name     string
		manifest *Manifest
		draft    string
		want     Result
	}{
		{
			name: "S-01 完全一致", manifest: std,
			draft: "桜あんぱんの販売は終了しました。",
			want:  wantFail(Hit{Term: "桜あんぱん", Line: 1, Excerpt: "桜あんぱんの販売は終了しました。"}),
		},
		{
			name: "S-02 ひらがな表記", manifest: std,
			draft: "さくらあんぱんはもうありません",
			want:  wantFail(Hit{Term: "さくらあんぱん", Line: 1, Excerpt: "さくらあんぱんはもうありません"}),
		},
		{
			name: "S-03 カタカナ→ひらがな相互一致", manifest: std,
			draft: "サクラアンパンフェア",
			want:  wantFail(Hit{Term: "さくらあんぱん", Line: 1, Excerpt: "サクラアンパンフェア"}),
		},
		{
			name: "S-04 半角カナ（NFKC）", manifest: std,
			draft: "ｻｸﾗあんぱん復活！",
			// 桜あんぱん は漢字を含み正規化で変化しないため、一致するのは さくらあんぱん のみ。
			want: wantFail(Hit{Term: "さくらあんぱん", Line: 1, Excerpt: "ｻｸﾗあんぱん復活！"}),
		},
		{
			name: "S-05 大文字", manifest: std,
			draft: "SAKURA ANPAN is back",
			want:  wantFail(Hit{Term: "sakura anpan", Line: 1, Excerpt: "SAKURA ANPAN is back"}),
		},
		{
			name: "S-06 大文字小文字の混在", manifest: std,
			draft: "Sakura Anpan",
			want:  wantFail(Hit{Term: "sakura anpan", Line: 1, Excerpt: "Sakura Anpan"}),
		},
		{
			name: "S-07 全角英字 + 全角空白", manifest: std,
			draft: "ＳＡＫＵＲＡ　ＡＮＰＡＮ",
			want:  wantFail(Hit{Term: "sakura anpan", Line: 1, Excerpt: "ＳＡＫＵＲＡ　ＡＮＰＡＮ"}),
		},
		{
			name: "S-08 複数 rejected・複数行", manifest: std,
			draft: "今週の朝フェア\n桜あんぱんは終了しました\nクロワッサン3種をご用意\n無料配布はありません",
			want: wantFail(
				Hit{Term: "桜あんぱん", Line: 2, Excerpt: "桜あんぱんは終了しました"},
				Hit{Term: "無料配布", Line: 4, Excerpt: "無料配布はありません"},
			),
		},
		{
			name: "S-09 部分文字列の合成では一致しない（陰性対照）", manifest: std,
			draft: "桜餅とあんぱんを別々に販売",
			want:  wantPass(),
		},
		{
			name: "S-10 正規化後に同形（同一 literal_terms 内・列挙順で最初）", manifest: sameFormOneRejected,
			draft: "さくらあんぱんセール",
			want:  wantFail(Hit{Term: "サクラアンパン", Line: 1, Excerpt: "さくらあんぱんセール"}),
		},
		{
			name: "S-11 正規化後に同形（rejected をまたぐ重複排除）", manifest: sameFormTwoRejected,
			draft: "さくらあんぱんセール",
			want:  wantFail(Hit{Term: "サクラアンパン", Line: 1, Excerpt: "さくらあんぱんセール"}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkScan(t, tc.manifest, tc.draft, tc.want)
		})
	}
}

// ---------------------------------------------------------------------------
// 5.3 TestScanVisibleExceptions — 例外の回数判定
// ---------------------------------------------------------------------------

func TestScanVisibleExceptions(t *testing.T) {
	std := mustManifest(t, standardManifest)

	// V-07 用: 番兵文字（U+FFFD）を literal_terms に含む manifest。
	// 例外マスクが文字置換ではなく区間管理であることを固定する。
	sentinelManifest := mustManifest(t, `{
  "schema_version": 1,
  "target_state": "朝フェア告知",
  "rejected": [
    {
      "id": "r1",
      "label": "置換文字",
      "literal_terms": ["�"],
      "concept": "文字化けの混入を検出するための語"
    }
  ],
  "visible_exceptions": [
    { "term": "カフェインレス", "max_occurrences": 1 }
  ]
}`)

	tests := []struct {
		name     string
		manifest *Manifest
		draft    string
		want     Result
	}{
		{
			name: "V-01 上限内は hit にしない", manifest: std,
			draft: "カフェインレスのコーヒーもご用意しています",
			want:  wantPass(),
		},
		{
			name: "V-02 超過分のみ FAIL", manifest: std,
			draft: "カフェインレスの豆を入荷しました\n朝の焙煎は7時から\nカフェインレスもご用意",
			want:  wantFail(Hit{Term: "カフェインレス", Line: 3, Excerpt: "カフェインレスもご用意"}),
		},
		{
			name: "V-03 出現回数は正規化一致で数える", manifest: std,
			draft: "ｶﾌｪｲﾝﾚｽの豆\nカフェインレスもあります",
			want:  wantFail(Hit{Term: "カフェインレス", Line: 2, Excerpt: "カフェインレスもあります"}),
		},
		{
			name: "V-04 境界値（ちょうど上限）", manifest: std,
			draft: "遅めの朝にどうぞ\n遅めの朝でも焼きたて",
			want:  wantPass(),
		},
		{
			name: "V-05 境界値+1", manifest: std,
			draft: "遅めの朝にどうぞ\n遅めの朝でも焼きたて\n遅めの朝のためのセット",
			want:  wantFail(Hit{Term: "遅めの朝", Line: 3, Excerpt: "遅めの朝のためのセット"}),
		},
		{
			name: "V-06 例外は出現義務ではない", manifest: std,
			draft: cleanDraft,
			want:  wantPass(),
		},
		{
			name: "V-07 例外マスクは区間管理（番兵文字を偽 hit させない）", manifest: sentinelManifest,
			draft: "カフェインレスのコーヒーもご用意しています",
			want:  wantPass(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkScan(t, tc.manifest, tc.draft, tc.want)
		})
	}
}

// TestScanExceptionMasksLiteral は例外語と禁止語が部分文字列の関係にある場合に、
// 免除した例外語の内部で禁止語が hit しないこと（DESIGN.md §6.2 のマスク）を固定する。
func TestScanExceptionMasksLiteral(t *testing.T) {
	m := mustManifest(t, `{
  "schema_version": 1,
  "target_state": "朝フェア告知",
  "rejected": [
    { "id": "r1", "label": "カフェ", "literal_terms": ["カフェ"], "concept": "却下された併設カフェの訴求" }
  ],
  "visible_exceptions": [
    { "term": "カフェインレス", "max_occurrences": 1 }
  ]
}`)

	t.Run("免除された例外語の内部は禁止語照合の対象外", func(t *testing.T) {
		checkScan(t, m, "カフェインレスの豆もあります", wantPass())
	})

	t.Run("例外語の外側に出た禁止語は検出する", func(t *testing.T) {
		checkScan(t, m, "カフェインレスの豆\n併設カフェは休業中",
			wantFail(Hit{Term: "カフェ", Line: 2, Excerpt: "併設カフェは休業中"}))
	})

	// 超過分もマスクの対象（DESIGN.md §6.2 手順2-3「免除・超過を問わず記録する」）。
	// 超過分をマスクしない実装だと、超過 hit に加えて内側の カフェ も hit してしまう。
	t.Run("超過した例外語の内部も禁止語照合の対象外", func(t *testing.T) {
		checkScan(t, m, "カフェインレスの豆\n本日もカフェインレス",
			wantFail(Hit{Term: "カフェインレス", Line: 2, Excerpt: "本日もカフェインレス"}))
	})

	// 「1 rune でも重なる出現を無視する」こと。完全包含だけを無視する実装だと レスの が hit する。
	t.Run("マスクと一部だけ重なる出現も無視する", func(t *testing.T) {
		partial := mustManifest(t, `{
  "schema_version": 1,
  "target_state": "朝フェア告知",
  "rejected": [
    { "id": "r1", "label": "レスの", "literal_terms": ["レスの"], "concept": "却下された語の断片" }
  ],
  "visible_exceptions": [
    { "term": "カフェインレス", "max_occurrences": 1 }
  ]
}`)
		checkScan(t, partial, "カフェインレスの豆もあります", wantPass())
	})

	// 例外語同士が重なる場合は manifest 記載順に早い者勝ち（DESIGN.md §6.2）。
	// 後続の例外語は先行例外のマスクと重なる出現を数えないため、上限 1 を超えない。
	t.Run("例外語同士のマスクは記載順で早い者勝ち", func(t *testing.T) {
		overlapping := mustManifest(t, `{
  "schema_version": 1,
  "target_state": "朝フェア告知",
  "rejected": [
    { "id": "r1", "label": "桜あんぱん", "literal_terms": ["桜あんぱん"], "concept": "却下された春季限定の菓子パン商品" }
  ],
  "visible_exceptions": [
    { "term": "カフェインレス", "max_occurrences": 1 },
    { "term": "インレ", "max_occurrences": 1 }
  ]
}`)
		checkScan(t, overlapping, "カフェインレスの豆\nインレスではない普通の豆", wantPass())
	})

	// 出現の数え方は非重複 greedy（見つけた長さ分進める）。
	// 重複カウントする実装だと あああ を 2 回と数えて上限 1 を超える。
	t.Run("出現回数は非重複で数える", func(t *testing.T) {
		repeated := mustManifest(t, `{
  "schema_version": 1,
  "target_state": "朝フェア告知",
  "rejected": [
    { "id": "r1", "label": "桜あんぱん", "literal_terms": ["桜あんぱん"], "concept": "却下された春季限定の菓子パン商品" }
  ],
  "visible_exceptions": [
    { "term": "ああ", "max_occurrences": 1 }
  ]
}`)
		checkScan(t, repeated, "あああ", wantPass())
	})
}

// ---------------------------------------------------------------------------
// 5.4 TestScanNegativeControl — 偽陽性のない陰性対照
// ---------------------------------------------------------------------------

func TestScanNegativeControl(t *testing.T) {
	noExceptions := mustManifest(t, `{
  "schema_version": 1,
  "target_state": "クロワッサン3種と自家焙煎コーヒーの朝フェア告知",
  "rejected": [
    {
      "id": "r1",
      "label": "桜あんぱん",
      "literal_terms": ["桜あんぱん", "さくらあんぱん", "sakura anpan"],
      "concept": "却下された春季限定の菓子パン商品"
    }
  ]
}`)

	tests := []struct {
		name     string
		manifest *Manifest
		draft    string
	}{
		{"C-01 正常稿", mustManifest(t, standardManifest), cleanDraft},
		{"C-02 visible_exceptions 省略", noExceptions, cleanDraft},
		{"C-03 空の初稿", mustManifest(t, standardManifest), ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Scan(tc.manifest, tc.draft)
			if !reflect.DeepEqual(got, wantPass()) {
				t.Errorf("Scan() = %+v, want %+v", got, wantPass())
			}
			// hits は null ではなく空配列としてシリアライズされること。
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("Result の直列化に失敗: %v", err)
			}
			if want := `{"pass":true,"hits":[]}`; string(b) != want {
				t.Errorf("json.Marshal(Result) = %s, want %s", b, want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 5.5 TestScanHitDetails — 行番号・excerpt の契約
// ---------------------------------------------------------------------------

func TestScanHitDetails(t *testing.T) {
	std := mustManifest(t, standardManifest)

	// D-06: trim 後ちょうど 120 rune / 121 rune の行を組み立てる。
	const match = "桜あんぱん" // 5 rune
	line120 := match + strings.Repeat("あ", 120-len([]rune(match)))
	line121 := match + strings.Repeat("あ", 121-len([]rune(match)))

	tests := []struct {
		name  string
		draft string
		want  Result
	}{
		{
			name:  "D-01 行番号は 1 始まり",
			draft: "今週の朝フェア\n桜あんぱんは終了しました\nクロワッサン3種をご用意",
			want:  wantFail(Hit{Term: "桜あんぱん", Line: 2, Excerpt: "桜あんぱんは終了しました"}),
		},
		{
			name:  "D-02 excerpt は正規化前の原文",
			draft: "今週の朝フェア\nｻｸﾗあんぱん復活！",
			want:  wantFail(Hit{Term: "さくらあんぱん", Line: 2, Excerpt: "ｻｸﾗあんぱん復活！"}),
		},
		{
			name:  "D-03 同一行の複数出現は出現ごとに 1 hit",
			draft: "桜あんぱんと桜あんぱん",
			want: wantFail(
				Hit{Term: "桜あんぱん", Line: 1, Excerpt: "桜あんぱんと桜あんぱん"},
				Hit{Term: "桜あんぱん", Line: 1, Excerpt: "桜あんぱんと桜あんぱん"},
			),
		},
		{
			name:  "D-04 行またぎは検出しない（現仕様の限界）",
			draft: "桜あん\nぱん",
			want:  wantPass(),
		},
		{
			name:  "D-05 CRLF 改行",
			draft: "今週の朝フェア\r\n桜あんぱんは終了しました\r\n",
			want:  wantFail(Hit{Term: "桜あんぱん", Line: 2, Excerpt: "桜あんぱんは終了しました"}),
		},
		{
			name:  "D-06a trim 後ちょうど 120 rune",
			draft: line120,
			want:  wantFail(Hit{Term: "桜あんぱん", Line: 1, Excerpt: line120}),
		},
		{
			name:  "D-06b trim 後 121 rune",
			draft: line121,
			want:  wantFail(Hit{Term: "桜あんぱん", Line: 1, Excerpt: string([]rune(line121)[:120]) + "…"}),
		},
		{
			name:  "D-07 前後の空白を除去",
			draft: "　  桜あんぱんは終了しました  　",
			want:  wantFail(Hit{Term: "桜あんぱん", Line: 1, Excerpt: "桜あんぱんは終了しました"}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			checkScan(t, std, tc.draft, tc.want)
		})
	}
}

// TestScanHitOrdering は hits が行番号昇順 → 行内出現位置昇順で整列することを固定する。
func TestScanHitOrdering(t *testing.T) {
	m := mustManifest(t, `{
  "schema_version": 1,
  "target_state": "朝フェア告知",
  "rejected": [
    { "id": "r2", "label": "無料配布", "literal_terms": ["無料配布"], "concept": "却下された無償プレゼント施策" },
    { "id": "r1", "label": "桜あんぱん", "literal_terms": ["桜あんぱん"], "concept": "却下された春季限定の菓子パン商品" }
  ]
}`)

	// 1 行目には 桜あんぱん（先頭）と 無料配布（後方）が並ぶ。manifest の記載順は 無料配布 が先だが、
	// hits は行内の出現位置順になる。
	draft := "桜あんぱんの無料配布\n無料配布は終了"
	want := wantFail(
		Hit{Term: "桜あんぱん", Line: 1, Excerpt: "桜あんぱんの無料配布"},
		Hit{Term: "無料配布", Line: 1, Excerpt: "桜あんぱんの無料配布"},
		Hit{Term: "無料配布", Line: 2, Excerpt: "無料配布は終了"},
	)
	checkScan(t, m, draft, want)
}

// TestScanHitTieBreak は行番号・行内オフセットが同値の hit の並び順を固定する。
// DESIGN.md §6.4 は第 3 キーを「検出順」とし、検出順は manifest の処理順
// （rejected の順 → 各 literal_terms の順）で定まる（§6.3）。
// 正規化後の形が異なる語は同一領域に重なって hit してよいため（§6.3）、
// さくら と さくらあんぱん は同じオフセット 0 で 2 件の hit になる。
func TestScanHitTieBreak(t *testing.T) {
	m := mustManifest(t, `{
  "schema_version": 1,
  "target_state": "朝フェア告知",
  "rejected": [
    { "id": "r1", "label": "さくら", "literal_terms": ["さくら"], "concept": "却下された春季の訴求" },
    { "id": "r2", "label": "さくらあんぱん", "literal_terms": ["さくらあんぱん"], "concept": "却下された春季限定の菓子パン商品" }
  ]
}`)

	const line = "さくらあんぱんセール"
	checkScan(t, m, line, wantFail(
		Hit{Term: "さくら", Line: 1, Excerpt: line},
		Hit{Term: "さくらあんぱん", Line: 1, Excerpt: line},
	))
}

// ---------------------------------------------------------------------------
// 5.6 TestManifestValidation — manifest 不正の網羅
// ---------------------------------------------------------------------------

func TestManifestValidation(t *testing.T) {
	tests := []struct {
		name    string
		src     func(t *testing.T) []byte
		wantErr bool
	}{
		{
			name:    "M-00 標準 manifest は検証を通る（陰性対照）",
			src:     func(*testing.T) []byte { return []byte(standardManifest) },
			wantErr: false,
		},
		{
			name:    "M-01 JSON として不正",
			src:     func(*testing.T) []byte { return []byte(`{`) },
			wantErr: true,
		},
		{
			name: "M-02 target_state 欠落",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { delete(m, "target_state") })
			},
			wantErr: true,
		},
		{
			name: "M-03 rejected が空配列",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { m["rejected"] = []any{} })
			},
			wantErr: true,
		},
		{
			name: "M-04 literal_terms が空配列",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) {
					rejectedAt(t, m, 0)["literal_terms"] = []any{}
				})
			},
			wantErr: true,
		},
		{
			name: "M-05 id のパターン違反",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { rejectedAt(t, m, 0)["id"] = "R1" })
			},
			wantErr: true,
		},
		{
			name: "M-06 max_occurrences が 0",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { exceptionAt(t, m, 0)["max_occurrences"] = 0 })
			},
			wantErr: true,
		},
		{
			name: "M-07 max_occurrences が文字列",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { exceptionAt(t, m, 0)["max_occurrences"] = "1" })
			},
			wantErr: true,
		},
		{
			name: "M-08 未知フィールド",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { m["note"] = "x" })
			},
			wantErr: true,
		},
		{
			name: "M-09 未知の schema_version",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { m["schema_version"] = 2 })
			},
			wantErr: true,
		},
		{
			name: "M-10 id の重複",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { rejectedAt(t, m, 1)["id"] = "r1" })
			},
			wantErr: true,
		},
		{
			name: "M-11 term と literal_terms の正規化後重複",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) {
					exceptionAt(t, m, 1)["term"] = "サクラアンパン"
				})
			},
			wantErr: true,
		},
		{
			name: "M-12 allowed_surfaces ⊄ surface",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) {
					exceptionAt(t, m, 0)["allowed_surfaces"] = []any{"footer"}
				})
			},
			wantErr: true,
		},
		{
			name: "M-13 surface 未宣言で allowed_surfaces あり",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { delete(m, "surface") })
			},
			wantErr: true,
		},
		{
			name: "M-14 literal_terms の重複",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) {
					rejectedAt(t, m, 0)["literal_terms"] = []any{"桜あんぱん", "桜あんぱん"}
				})
			},
			wantErr: true,
		},
		{
			name: "M-15a literal_terms に改行",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) {
					rejectedAt(t, m, 0)["literal_terms"] = []any{"桜\nあんぱん"}
				})
			},
			wantErr: true,
		},
		{
			name: "M-15b visible_exceptions[].term に改行",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) {
					exceptionAt(t, m, 0)["term"] = "カフェ\nインレス"
				})
			},
			wantErr: true,
		},
		{
			name:    "M-16a 単一 JSON 値の後に別の値",
			src:     func(*testing.T) []byte { return []byte(standardManifest + standardManifest) },
			wantErr: true,
		},
		{
			name:    "M-16b 単一 JSON 値の後にゴミ",
			src:     func(*testing.T) []byte { return []byte(standardManifest + " x") },
			wantErr: true,
		},
		{
			name: "M-17a visible_exceptions が明示的 null",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { m["visible_exceptions"] = nil })
			},
			wantErr: true,
		},
		{
			name: "M-17b surface が明示的 null",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { m["surface"] = nil })
			},
			wantErr: true,
		},
		{
			name: "M-18 schema_version 欠落",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { delete(m, "schema_version") })
			},
			wantErr: true,
		},
		{
			name: "M-19 max_occurrences が非整数",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { exceptionAt(t, m, 0)["max_occurrences"] = 1.5 })
			},
			wantErr: true,
		},
		{
			name: "M-20 rejected[] の内側に未知フィールド",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { rejectedAt(t, m, 0)["note"] = "x" })
			},
			wantErr: true,
		},
		{
			name: "M-21 visible_exceptions[] の内側に未知フィールド",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { exceptionAt(t, m, 0)["note"] = "x" })
			},
			wantErr: true,
		},
		{
			name: "M-22 target_state が上限超過（2001 rune）",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { m["target_state"] = strings.Repeat("あ", 2001) })
			},
			wantErr: true,
		},
		{
			name: "M-23 label が上限超過（201 rune）",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) {
					rejectedAt(t, m, 0)["label"] = strings.Repeat("あ", 201)
				})
			},
			wantErr: true,
		},
		{
			name: "M-24 concept が上限超過（501 rune）",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) {
					rejectedAt(t, m, 0)["concept"] = strings.Repeat("あ", 501)
				})
			},
			wantErr: true,
		},
		{
			name: "M-25 literal_terms の要素が上限超過（201 rune）",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) {
					rejectedAt(t, m, 0)["literal_terms"] = []any{strings.Repeat("あ", 201)}
				})
			},
			wantErr: true,
		},
		{
			name: "M-26 visible_exceptions[].term が上限超過（201 rune）",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) {
					exceptionAt(t, m, 0)["term"] = strings.Repeat("あ", 201)
				})
			},
			wantErr: true,
		},
		{
			name: "M-27 target_state が空文字（下限違反）",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { m["target_state"] = "" })
			},
			wantErr: true,
		},
		{
			name: "M-28 allowed_surfaces が空配列",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { exceptionAt(t, m, 0)["allowed_surfaces"] = []any{} })
			},
			wantErr: true,
		},
		{
			name: "M-29 surface の重複",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { m["surface"] = []any{"body", "body"} })
			},
			wantErr: true,
		},
		{
			name: "M-30 allowed_surfaces の重複",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) {
					exceptionAt(t, m, 0)["allowed_surfaces"] = []any{"body", "body"}
				})
			},
			wantErr: true,
		},
		{
			name: "M-31 必須フィールドの明示的 null（rejected）",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { m["rejected"] = nil })
			},
			wantErr: true,
		},
		{
			name: "M-32 必須フィールドの明示的 null（target_state）",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { m["target_state"] = nil })
			},
			wantErr: true,
		},
		{
			name: "M-33 必須フィールドの明示的 null（literal_terms）",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { rejectedAt(t, m, 0)["literal_terms"] = nil })
			},
			wantErr: true,
		},
		{
			name: "M-34 任意フィールドの明示的 null（allowed_surfaces）",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { exceptionAt(t, m, 0)["allowed_surfaces"] = nil })
			},
			wantErr: true,
		},
		{
			name: "M-35 任意フィールドの明示的 null（reason）",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { exceptionAt(t, m, 0)["reason"] = nil })
			},
			wantErr: true,
		},
		{
			name: "M-36 rejected[] の要素がオブジェクトでない",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) { m["rejected"] = []any{"r1"} })
			},
			wantErr: true,
		},
		{
			name: "M-37 境界値: 各フィールドが上限ちょうどなら通る（陰性対照）",
			src: func(t *testing.T) []byte {
				return brokenManifest(t, func(m map[string]any) {
					m["target_state"] = strings.Repeat("あ", 2000)
					rejectedAt(t, m, 0)["label"] = strings.Repeat("あ", 200)
					rejectedAt(t, m, 0)["concept"] = strings.Repeat("あ", 500)
				})
			},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src(t)

			_, err := ParseManifest(src)
			switch {
			case tc.wantErr && err == nil:
				t.Fatalf("ParseManifest() はエラーを返すべきだが nil を返した")
			case !tc.wantErr && err != nil:
				t.Fatalf("ParseManifest() で予期しないエラー: %v", err)
			}

			// run() 経由でも manifest カテゴリの失敗（exit 3 / stdout 空）になることを確かめる。
			manifestPath := writeTemp(t, "manifest.json", string(src))
			draftPath := writeTemp(t, "draft.txt", cleanDraft)
			var stdout, stderr bytes.Buffer
			code := run([]string{"--manifest", manifestPath, "--draft", draftPath}, &stdout, &stderr)

			if tc.wantErr {
				checkFailure(t, categoryManifest, code, stdout.String(), stderr.String())
			} else if code != 0 {
				t.Errorf("run() = %d, want 0（stderr: %s）", code, stderr.String())
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 5.7 TestRunExitCodes — 終了コードと stdout JSON
// ---------------------------------------------------------------------------

func TestRunExitCodes(t *testing.T) {
	t.Run("E-01 PASS", func(t *testing.T) {
		manifestPath := writeTemp(t, "manifest.json", standardManifest)
		draftPath := writeTemp(t, "draft.txt", cleanDraft)

		var stdout, stderr bytes.Buffer
		code := run([]string{"--manifest", manifestPath, "--draft", draftPath}, &stdout, &stderr)

		if code != 0 {
			t.Errorf("run() = %d, want 0（stderr: %s）", code, stderr.String())
		}
		if got, want := strings.TrimSpace(stdout.String()), `{"pass":true,"hits":[]}`; got != want {
			t.Errorf("stdout = %s, want %s", got, want)
		}
	})

	t.Run("E-02 FAIL", func(t *testing.T) {
		manifestPath := writeTemp(t, "manifest.json", standardManifest)
		draftPath := writeTemp(t, "draft.txt", "桜あんぱんの販売は終了しました。")

		var stdout, stderr bytes.Buffer
		code := run([]string{"--manifest", manifestPath, "--draft", draftPath}, &stdout, &stderr)

		if code != 1 {
			t.Errorf("run() = %d, want 1（stderr: %s）", code, stderr.String())
		}
		// 生文字列で比較して JSON タグ（term / line / excerpt）まで固定する。
		// json.Unmarshal はフィールド名を大文字小文字を区別せずに照合するため、
		// 構造体へ読み戻す比較ではタグの無い実装（Term / Line / Excerpt）も通ってしまう。
		want := `{"pass":false,"hits":[{"term":"桜あんぱん","line":1,"excerpt":"桜あんぱんの販売は終了しました。"}]}`
		if got := strings.TrimSpace(stdout.String()); got != want {
			t.Errorf("stdout = %s\nwant %s", got, want)
		}
	})

	// E-09: SetEscapeHTML(false) の契約（DESIGN.md §2.3）。
	// encoding/json は既定で < > & を \u003c / \u003e / \u0026 へ書き換える。
	// 非 ASCII はもともとエスケープしないため、日本語が出ることを見ても本契約は検証できない。
	t.Run("E-09 HTML エスケープを行わない", func(t *testing.T) {
		manifestPath := writeTemp(t, "manifest.json", standardManifest)
		draftPath := writeTemp(t, "draft.txt", `<b>桜あんぱん</b> & コーヒー`)

		var stdout, stderr bytes.Buffer
		code := run([]string{"--manifest", manifestPath, "--draft", draftPath}, &stdout, &stderr)

		if code != 1 {
			t.Errorf("run() = %d, want 1（stderr: %s）", code, stderr.String())
		}
		want := `{"pass":false,"hits":[{"term":"桜あんぱん","line":1,"excerpt":"<b>桜あんぱん</b> & コーヒー"}]}`
		if got := strings.TrimSpace(stdout.String()); got != want {
			t.Errorf("stdout = %s\nwant %s", got, want)
		}
	})

	invalidArgs := []struct {
		name     string
		args     func(t *testing.T) []string
		category string
	}{
		{
			name: "E-03 manifest 不正",
			args: func(t *testing.T) []string {
				return []string{
					"--manifest", writeTemp(t, "manifest.json", "{"),
					"--draft", writeTemp(t, "draft.txt", cleanDraft),
				}
			},
			category: categoryManifest,
		},
		{
			name: "E-04 draft のファイルが存在しない",
			args: func(t *testing.T) []string {
				return []string{
					"--manifest", writeTemp(t, "manifest.json", standardManifest),
					"--draft", filepath.Join(t.TempDir(), "missing-draft.txt"),
				}
			},
			category: categoryDraft,
		},
		{
			name: "E-05 引数不足",
			args: func(t *testing.T) []string {
				return []string{"--manifest", writeTemp(t, "manifest.json", standardManifest)}
			},
			category: categoryUsage,
		},
		{
			name: "E-06 manifest のファイルが存在しない",
			args: func(t *testing.T) []string {
				return []string{
					"--manifest", filepath.Join(t.TempDir(), "missing-manifest.json"),
					"--draft", writeTemp(t, "draft.txt", cleanDraft),
				}
			},
			category: categoryManifest,
		},
		{
			name: "E-07 未知のフラグ",
			args: func(t *testing.T) []string {
				return []string{
					"--manifest", writeTemp(t, "manifest.json", standardManifest),
					"--draft", writeTemp(t, "draft.txt", cleanDraft),
					"--surface", "body",
				}
			},
			category: categoryUsage,
		},
		{
			name: "E-08 余分な位置引数",
			args: func(t *testing.T) []string {
				return []string{
					"--manifest", writeTemp(t, "manifest.json", standardManifest),
					"--draft", writeTemp(t, "draft.txt", cleanDraft),
					"extra.txt",
				}
			},
			category: categoryUsage,
		},
	}

	for _, tc := range invalidArgs {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tc.args(t), &stdout, &stderr)

			checkFailure(t, tc.category, code, stdout.String(), stderr.String())
		})
	}
}

// ---------------------------------------------------------------------------
// 5.8 TestInternalErrors — internal カテゴリ（exit 5）
//
// 0/1 は「検査の判定」専用で、判定を伝達できなかった失敗は 0/1 を返してはならない
// （scan/DESIGN.md §2.2）。Go ランタイムは回復しない panic を exit 2 で終了させるため、
// guard() が無いと usage（引数不正）と区別できなくなる。
// ---------------------------------------------------------------------------

// failingWriter は書き込みが必ず失敗する io.Writer。stdout へ書けない状況を作る。
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("書き込みに失敗した") }

func TestInternalErrors(t *testing.T) {
	t.Run("I-01 stdout へ書けない場合は internal（判定できていても 0/1 を返さない）", func(t *testing.T) {
		manifestPath := writeTemp(t, "manifest.json", standardManifest)
		draftPath := writeTemp(t, "draft.txt", cleanDraft) // 判定自体は PASS になる入力

		var stderr bytes.Buffer
		code := run([]string{"--manifest", manifestPath, "--draft", draftPath}, failingWriter{}, &stderr)

		checkFailure(t, categoryInternal, code, "", stderr.String())
	})

	t.Run("I-02 panic は internal へ写す（usage の exit 2 と衝突させない）", func(t *testing.T) {
		var stderr bytes.Buffer
		code := guard(&stderr, func() int { panic("予期しない失敗") })

		checkFailure(t, categoryInternal, code, "", stderr.String())
		if code == failureExit[categoryUsage] {
			t.Errorf("panic が usage（exit %d）と衝突している", failureExit[categoryUsage])
		}
		// 診断のためスタックトレースを残す（§2.4 の internal のみの例外）。
		if !strings.Contains(stderr.String(), "runtime/debug.Stack") {
			t.Errorf("stderr にスタックトレースがない: %s", stderr.String())
		}
	})

	t.Run("I-03 panic しなければ guard は戻り値を素通しする", func(t *testing.T) {
		for _, want := range []int{0, 1, 2, 3, 4} {
			var stderr bytes.Buffer
			if got := guard(&stderr, func() int { return want }); got != want {
				t.Errorf("guard() = %d, want %d", got, want)
			}
			if stderr.Len() != 0 {
				t.Errorf("panic していないのに stderr へ出力した: %s", stderr.String())
			}
		}
	})
}
