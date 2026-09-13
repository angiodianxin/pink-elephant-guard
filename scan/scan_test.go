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
//	func run(args []string, stdout, stderr io.Writer) int  // main.go（戻り値がそのまま終了コード）
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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

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

// pass / fail は期待 Result を組み立てる糖衣。hits は必ず空配列（null ではない）。
func pass() Result { return Result{Pass: true, Hits: []Hit{}} }

func fail(hits ...Hit) Result { return Result{Pass: false, Hits: hits} }

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
			want:  fail(Hit{Term: "桜あんぱん", Line: 1, Excerpt: "桜あんぱんの販売は終了しました。"}),
		},
		{
			name: "S-02 ひらがな表記", manifest: std,
			draft: "さくらあんぱんはもうありません",
			want:  fail(Hit{Term: "さくらあんぱん", Line: 1, Excerpt: "さくらあんぱんはもうありません"}),
		},
		{
			name: "S-03 カタカナ→ひらがな相互一致", manifest: std,
			draft: "サクラアンパンフェア",
			want:  fail(Hit{Term: "さくらあんぱん", Line: 1, Excerpt: "サクラアンパンフェア"}),
		},
		{
			name: "S-04 半角カナ（NFKC）", manifest: std,
			draft: "ｻｸﾗあんぱん復活！",
			// 桜あんぱん は漢字を含み正規化で変化しないため、一致するのは さくらあんぱん のみ。
			want: fail(Hit{Term: "さくらあんぱん", Line: 1, Excerpt: "ｻｸﾗあんぱん復活！"}),
		},
		{
			name: "S-05 大文字", manifest: std,
			draft: "SAKURA ANPAN is back",
			want:  fail(Hit{Term: "sakura anpan", Line: 1, Excerpt: "SAKURA ANPAN is back"}),
		},
		{
			name: "S-06 大文字小文字の混在", manifest: std,
			draft: "Sakura Anpan",
			want:  fail(Hit{Term: "sakura anpan", Line: 1, Excerpt: "Sakura Anpan"}),
		},
		{
			name: "S-07 全角英字 + 全角空白", manifest: std,
			draft: "ＳＡＫＵＲＡ　ＡＮＰＡＮ",
			want:  fail(Hit{Term: "sakura anpan", Line: 1, Excerpt: "ＳＡＫＵＲＡ　ＡＮＰＡＮ"}),
		},
		{
			name: "S-08 複数 rejected・複数行", manifest: std,
			draft: "今週の朝フェア\n桜あんぱんは終了しました\nクロワッサン3種をご用意\n無料配布はありません",
			want: fail(
				Hit{Term: "桜あんぱん", Line: 2, Excerpt: "桜あんぱんは終了しました"},
				Hit{Term: "無料配布", Line: 4, Excerpt: "無料配布はありません"},
			),
		},
		{
			name: "S-09 部分文字列の合成では一致しない（陰性対照）", manifest: std,
			draft: "桜餅とあんぱんを別々に販売",
			want:  pass(),
		},
		{
			name: "S-10 正規化後に同形（同一 literal_terms 内・列挙順で最初）", manifest: sameFormOneRejected,
			draft: "さくらあんぱんセール",
			want:  fail(Hit{Term: "サクラアンパン", Line: 1, Excerpt: "さくらあんぱんセール"}),
		},
		{
			name: "S-11 正規化後に同形（rejected をまたぐ重複排除）", manifest: sameFormTwoRejected,
			draft: "さくらあんぱんセール",
			want:  fail(Hit{Term: "サクラアンパン", Line: 1, Excerpt: "さくらあんぱんセール"}),
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
			want:  pass(),
		},
		{
			name: "V-02 超過分のみ FAIL", manifest: std,
			draft: "カフェインレスの豆を入荷しました\n朝の焙煎は7時から\nカフェインレスもご用意",
			want:  fail(Hit{Term: "カフェインレス", Line: 3, Excerpt: "カフェインレスもご用意"}),
		},
		{
			name: "V-03 出現回数は正規化一致で数える", manifest: std,
			draft: "ｶﾌｪｲﾝﾚｽの豆\nカフェインレスもあります",
			want:  fail(Hit{Term: "カフェインレス", Line: 2, Excerpt: "カフェインレスもあります"}),
		},
		{
			name: "V-04 境界値（ちょうど上限）", manifest: std,
			draft: "遅めの朝にどうぞ\n遅めの朝でも焼きたて",
			want:  pass(),
		},
		{
			name: "V-05 境界値+1", manifest: std,
			draft: "遅めの朝にどうぞ\n遅めの朝でも焼きたて\n遅めの朝のためのセット",
			want:  fail(Hit{Term: "遅めの朝", Line: 3, Excerpt: "遅めの朝のためのセット"}),
		},
		{
			name: "V-06 例外は出現義務ではない", manifest: std,
			draft: cleanDraft,
			want:  pass(),
		},
		{
			name: "V-07 例外マスクは区間管理（番兵文字を偽 hit させない）", manifest: sentinelManifest,
			draft: "カフェインレスのコーヒーもご用意しています",
			want:  pass(),
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
		checkScan(t, m, "カフェインレスの豆もあります", pass())
	})

	t.Run("例外語の外側に出た禁止語は検出する", func(t *testing.T) {
		checkScan(t, m, "カフェインレスの豆\n併設カフェは休業中",
			fail(Hit{Term: "カフェ", Line: 2, Excerpt: "併設カフェは休業中"}))
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
			if !reflect.DeepEqual(got, pass()) {
				t.Errorf("Scan() = %+v, want %+v", got, pass())
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
			want:  fail(Hit{Term: "桜あんぱん", Line: 2, Excerpt: "桜あんぱんは終了しました"}),
		},
		{
			name:  "D-02 excerpt は正規化前の原文",
			draft: "今週の朝フェア\nｻｸﾗあんぱん復活！",
			want:  fail(Hit{Term: "さくらあんぱん", Line: 2, Excerpt: "ｻｸﾗあんぱん復活！"}),
		},
		{
			name:  "D-03 同一行の複数出現は出現ごとに 1 hit",
			draft: "桜あんぱんと桜あんぱん",
			want: fail(
				Hit{Term: "桜あんぱん", Line: 1, Excerpt: "桜あんぱんと桜あんぱん"},
				Hit{Term: "桜あんぱん", Line: 1, Excerpt: "桜あんぱんと桜あんぱん"},
			),
		},
		{
			name:  "D-04 行またぎは検出しない（現仕様の限界）",
			draft: "桜あん\nぱん",
			want:  pass(),
		},
		{
			name:  "D-05 CRLF 改行",
			draft: "今週の朝フェア\r\n桜あんぱんは終了しました\r\n",
			want:  fail(Hit{Term: "桜あんぱん", Line: 2, Excerpt: "桜あんぱんは終了しました"}),
		},
		{
			name:  "D-06a trim 後ちょうど 120 rune",
			draft: line120,
			want:  fail(Hit{Term: "桜あんぱん", Line: 1, Excerpt: line120}),
		},
		{
			name:  "D-06b trim 後 121 rune",
			draft: line121,
			want:  fail(Hit{Term: "桜あんぱん", Line: 1, Excerpt: string([]rune(line121)[:120]) + "…"}),
		},
		{
			name:  "D-07 前後の空白を除去",
			draft: "　  桜あんぱんは終了しました  　",
			want:  fail(Hit{Term: "桜あんぱん", Line: 1, Excerpt: "桜あんぱんは終了しました"}),
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
	want := fail(
		Hit{Term: "桜あんぱん", Line: 1, Excerpt: "桜あんぱんの無料配布"},
		Hit{Term: "無料配布", Line: 1, Excerpt: "桜あんぱんの無料配布"},
		Hit{Term: "無料配布", Line: 2, Excerpt: "無料配布は終了"},
	)
	checkScan(t, m, draft, want)
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

			// run() 経由でも同じ判定（exit 2 / stdout 空）になることを確かめる。
			manifestPath := writeTemp(t, "manifest.json", string(src))
			draftPath := writeTemp(t, "draft.txt", cleanDraft)
			var stdout, stderr bytes.Buffer
			code := run([]string{"--manifest", manifestPath, "--draft", draftPath}, &stdout, &stderr)

			if tc.wantErr {
				if code != 2 {
					t.Errorf("run() = %d, want 2（stderr: %s）", code, stderr.String())
				}
				if stdout.Len() != 0 {
					t.Errorf("exit 2 では stdout へ出力しない, got %q", stdout.String())
				}
				if !strings.HasPrefix(stderr.String(), "pink-elephant-scan: manifest error: ") {
					t.Errorf("stderr = %q, want manifest カテゴリの接頭辞", stderr.String())
				}
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
		var got Result
		if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
			t.Fatalf("stdout が JSON として読めない（%q）: %v", stdout.String(), err)
		}
		want := fail(Hit{Term: "桜あんぱん", Line: 1, Excerpt: "桜あんぱんの販売は終了しました。"})
		if !reflect.DeepEqual(got, want) {
			t.Errorf("stdout の Result = %+v, want %+v", got, want)
		}
		// 日本語は \uXXXX へエスケープせずそのまま出す。
		if !strings.Contains(stdout.String(), "桜あんぱん") {
			t.Errorf("stdout = %q, want 日本語をそのまま含む", stdout.String())
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
			category: "manifest",
		},
		{
			name: "E-04 draft のファイルが存在しない",
			args: func(t *testing.T) []string {
				return []string{
					"--manifest", writeTemp(t, "manifest.json", standardManifest),
					"--draft", filepath.Join(t.TempDir(), "missing-draft.txt"),
				}
			},
			category: "draft",
		},
		{
			name: "E-05 引数不足",
			args: func(t *testing.T) []string {
				return []string{"--manifest", writeTemp(t, "manifest.json", standardManifest)}
			},
			category: "usage",
		},
		{
			name: "E-06 manifest のファイルが存在しない",
			args: func(t *testing.T) []string {
				return []string{
					"--manifest", filepath.Join(t.TempDir(), "missing-manifest.json"),
					"--draft", writeTemp(t, "draft.txt", cleanDraft),
				}
			},
			category: "manifest",
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
			category: "usage",
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
			category: "usage",
		},
	}

	for _, tc := range invalidArgs {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tc.args(t), &stdout, &stderr)

			if code != 2 {
				t.Errorf("run() = %d, want 2（stderr: %s）", code, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Errorf("exit 2 では検査結果 JSON を出力しない, got %q", stdout.String())
			}
			// stderr の詳細文言は後方互換の対象外。カテゴリ接頭辞までを検証する。
			wantPrefix := "pink-elephant-scan: " + tc.category + " error: "
			if !strings.HasPrefix(stderr.String(), wantPrefix) {
				t.Errorf("stderr = %q, want 接頭辞 %q", stderr.String(), wantPrefix)
			}
		})
	}
}
