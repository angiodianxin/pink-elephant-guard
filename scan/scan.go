package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// supportedSchemaVersion は本実装が受け付ける manifest の版。
// 未知の版は差し戻す（scan/DESIGN.md §5、schema/manifest.schema.json の const: 1）。
const supportedSchemaVersion = 1

// excerptMaxRunes は hits[].excerpt の最大長（scan/DESIGN.md §2.3）。超過分は切り詰めて省略記号を付す。
const excerptMaxRunes = 120

const excerptEllipsis = "…"

// rejectedIDPattern は schema/manifest.schema.json の rejected[].id の pattern を写したもの。
var rejectedIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// ---------------------------------------------------------------------------
// manifest の型（検証済みの値のみを保持する）
// ---------------------------------------------------------------------------

// Manifest は検証を通った pink-elephant-manifest.json。
// L1 が判定に使うのは Rejected[].LiteralTerms と VisibleExceptions だけで、
// 残りは検証のためだけに読む（scan/DESIGN.md §1 のスコープ外項目）。
type Manifest struct {
	TargetState       string
	Rejected          []Rejected
	VisibleExceptions []VisibleException
	Surface           []string
}

// Rejected は却下された要素 1 件。
type Rejected struct {
	ID           string
	Label        string
	LiteralTerms []string
	Concept      string
}

// VisibleException は完成面に残してよい語 1 件。
type VisibleException struct {
	Term            string
	MaxOccurrences  int
	AllowedSurfaces []string
	Reason          string
}

// Result は検査結果。stdout へ 1 行の JSON として出す（scan/DESIGN.md §2.3）。
type Result struct {
	Pass bool  `json:"pass"`
	Hits []Hit `json:"hits"`
}

// Hit は検出された 1 出現。
type Hit struct {
	Term    string `json:"term"`
	Line    int    `json:"line"`
	Excerpt string `json:"excerpt"`
}

// ---------------------------------------------------------------------------
// manifest の生表現
//
// 任意フィールドの「欠落」と「明示的な null」を区別する必要があるため
// （scan/DESIGN.md §5。JSON Schema では任意フィールドも値が null なら不正）、
// 一旦すべて json.RawMessage で受けてから手書きで検証する。
// 非ポインタの json.RawMessage は欠落で len 0、明示的 null で "null" になる。
// ---------------------------------------------------------------------------

type rawManifest struct {
	SchemaVersion     json.RawMessage `json:"schema_version"`
	TargetState       json.RawMessage `json:"target_state"`
	Rejected          json.RawMessage `json:"rejected"`
	VisibleExceptions json.RawMessage `json:"visible_exceptions"`
	Surface           json.RawMessage `json:"surface"`
}

type rawRejected struct {
	ID           json.RawMessage `json:"id"`
	Label        json.RawMessage `json:"label"`
	LiteralTerms json.RawMessage `json:"literal_terms"`
	Concept      json.RawMessage `json:"concept"`
}

type rawException struct {
	Term            json.RawMessage `json:"term"`
	MaxOccurrences  json.RawMessage `json:"max_occurrences"`
	AllowedSurfaces json.RawMessage `json:"allowed_surfaces"`
	Reason          json.RawMessage `json:"reason"`
}

// ---------------------------------------------------------------------------
// 検証の下請け
// ---------------------------------------------------------------------------

// decodeStrict は未知フィールドを拒否しつつ raw を v へデコードし、
// 単一の JSON 値だけで入力が尽きていることを確認する。
// 2 回目の Decode が io.EOF を返さない入力（`{...}{...}`・`{...} x` 等）は不正とする
// （scan/DESIGN.md §5 手順 1）。
func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	switch err := dec.Decode(new(json.RawMessage)); {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return errors.New("単一の JSON 値ではない（末尾に別の値が続く）")
	default:
		return fmt.Errorf("単一の JSON 値ではない: %w", err)
	}
}

func fieldPresent(raw json.RawMessage) bool { return len(raw) > 0 }

func fieldIsNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// requireField は必須フィールドの存在を確かめる。明示的な null も不正。
func requireField(raw json.RawMessage, path string) error {
	switch {
	case !fieldPresent(raw):
		return fmt.Errorf("%s がない", path)
	case fieldIsNull(raw):
		return fmt.Errorf("%s が null", path)
	}
	return nil
}

// optionalField は任意フィールドの存在を返す。欠落は許すが、明示的な null は不正とする。
func optionalField(raw json.RawMessage, path string) (bool, error) {
	switch {
	case !fieldPresent(raw):
		return false, nil
	case fieldIsNull(raw):
		return false, fmt.Errorf("%s が null（省略と null は区別する）", path)
	}
	return true, nil
}

// parseString は文字列フィールドを取り出し、rune 数の下限・上限を確かめる。
func parseString(raw json.RawMessage, path string, minRunes, maxRunes int) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("%s は文字列でなければならない: %w", path, err)
	}
	if n := len([]rune(s)); n < minRunes || n > maxRunes {
		return "", fmt.Errorf("%s の長さ %d は %d〜%d の範囲外", path, n, minRunes, maxRunes)
	}
	return s, nil
}

// stringArrayRules は文字列配列フィールドのスキーマ制約。
type stringArrayRules struct {
	minItems    int
	minRunes    int
	maxRunes    int
	uniqueItems bool
	noNewline   bool // schema の pattern ^[^\r\n]+$（照合が行単位のため改行を含む語は不正）
}

func parseStringArray(raw json.RawMessage, path string, rules stringArrayRules) ([]string, error) {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, fmt.Errorf("%s は配列でなければならない: %w", path, err)
	}
	if len(elems) < rules.minItems {
		return nil, fmt.Errorf("%s の要素数 %d は下限 %d 未満", path, len(elems), rules.minItems)
	}
	out := make([]string, 0, len(elems))
	seen := make(map[string]struct{}, len(elems))
	for i, elem := range elems {
		itemPath := fmt.Sprintf("%s[%d]", path, i)
		if err := requireField(elem, itemPath); err != nil {
			return nil, err
		}
		s, err := parseString(elem, itemPath, rules.minRunes, rules.maxRunes)
		if err != nil {
			return nil, err
		}
		if rules.noNewline && strings.ContainsAny(s, "\r\n") {
			return nil, fmt.Errorf("%s が改行を含む（照合は行単位のため不正）", itemPath)
		}
		if rules.uniqueItems {
			if _, dup := seen[s]; dup {
				return nil, fmt.Errorf("%s %q が重複している", path, s)
			}
			seen[s] = struct{}{}
		}
		out = append(out, s)
	}
	return out, nil
}

// parseInt は整数フィールドを取り出す。
// json.Number は JSON 文字列 "1" も受け付けてしまうため、先頭バイトで数値リテラルであることを確かめる。
func parseInt(raw json.RawMessage, path string) (int, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] == '"' {
		return 0, fmt.Errorf("%s は整数でなければならない", path)
	}
	var n json.Number
	if err := json.Unmarshal(trimmed, &n); err != nil {
		return 0, fmt.Errorf("%s は整数でなければならない: %w", path, err)
	}
	v, err := n.Int64()
	if err != nil {
		return 0, fmt.Errorf("%s は整数でなければならない: %s", path, n)
	}
	return int(v), nil
}

// ---------------------------------------------------------------------------
// ParseManifest
// ---------------------------------------------------------------------------

// ParseManifest は manifest を検証して読み込む。
// schema/manifest.schema.json の制約（§5 手順 2）と §7.2 の整合性制約（手順 3）を
// 手書きで写して順に確かめ、最初の違反で error を返す。呼び出し元はこれを exit 3 に写す。
func ParseManifest(b []byte) (*Manifest, error) {
	var raw rawManifest
	if err := decodeStrict(b, &raw); err != nil {
		return nil, err
	}

	m := &Manifest{}

	// schema_version: 欠落と「未知の版」はどちらも不正だが、メッセージを区別する。
	if !fieldPresent(raw.SchemaVersion) {
		return nil, errors.New("schema_version がない")
	}
	version, err := parseInt(raw.SchemaVersion, "schema_version")
	if err != nil || version != supportedSchemaVersion {
		return nil, fmt.Errorf("unsupported schema_version: %s", strings.TrimSpace(string(raw.SchemaVersion)))
	}

	// target_state
	if err := requireField(raw.TargetState, "target_state"); err != nil {
		return nil, err
	}
	if m.TargetState, err = parseString(raw.TargetState, "target_state", 1, 2000); err != nil {
		return nil, err
	}

	// rejected
	if err := requireField(raw.Rejected, "rejected"); err != nil {
		return nil, err
	}
	if m.Rejected, err = parseRejected(raw.Rejected); err != nil {
		return nil, err
	}

	// visible_exceptions（任意）
	if ok, err := optionalField(raw.VisibleExceptions, "visible_exceptions"); err != nil {
		return nil, err
	} else if ok {
		if m.VisibleExceptions, err = parseExceptions(raw.VisibleExceptions); err != nil {
			return nil, err
		}
	}

	// surface（任意）
	surfaceDeclared, err := optionalField(raw.Surface, "surface")
	if err != nil {
		return nil, err
	}
	if surfaceDeclared {
		m.Surface, err = parseStringArray(raw.Surface, "surface", stringArrayRules{
			minRunes: 1, maxRunes: 50, uniqueItems: true,
		})
		if err != nil {
			return nil, err
		}
	}

	if err := checkConsistency(m, surfaceDeclared); err != nil {
		return nil, err
	}
	return m, nil
}

func parseRejected(raw json.RawMessage) ([]Rejected, error) {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, fmt.Errorf("rejected は配列でなければならない: %w", err)
	}
	if len(elems) < 1 {
		return nil, errors.New("rejected は 1 件以上でなければならない")
	}

	out := make([]Rejected, 0, len(elems))
	for i, elem := range elems {
		path := fmt.Sprintf("rejected[%d]", i)
		var rr rawRejected
		if err := decodeStrict(elem, &rr); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		var r Rejected
		if err := requireField(rr.ID, path+".id"); err != nil {
			return nil, err
		}
		id, err := parseString(rr.ID, path+".id", 1, 32)
		if err != nil {
			return nil, err
		}
		if !rejectedIDPattern.MatchString(id) {
			return nil, fmt.Errorf("%s.id %q が書式 %s に合わない", path, id, rejectedIDPattern)
		}
		r.ID = id

		if err := requireField(rr.Label, path+".label"); err != nil {
			return nil, err
		}
		if r.Label, err = parseString(rr.Label, path+".label", 1, 200); err != nil {
			return nil, err
		}

		if err := requireField(rr.LiteralTerms, path+".literal_terms"); err != nil {
			return nil, err
		}
		r.LiteralTerms, err = parseStringArray(rr.LiteralTerms, path+".literal_terms", stringArrayRules{
			minItems: 1, minRunes: 1, maxRunes: 200, uniqueItems: true, noNewline: true,
		})
		if err != nil {
			return nil, err
		}

		if err := requireField(rr.Concept, path+".concept"); err != nil {
			return nil, err
		}
		if r.Concept, err = parseString(rr.Concept, path+".concept", 1, 500); err != nil {
			return nil, err
		}

		out = append(out, r)
	}
	return out, nil
}

func parseExceptions(raw json.RawMessage) ([]VisibleException, error) {
	var elems []json.RawMessage
	if err := json.Unmarshal(raw, &elems); err != nil {
		return nil, fmt.Errorf("visible_exceptions は配列でなければならない: %w", err)
	}

	out := make([]VisibleException, 0, len(elems))
	for i, elem := range elems {
		path := fmt.Sprintf("visible_exceptions[%d]", i)
		var re rawException
		if err := decodeStrict(elem, &re); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		var e VisibleException
		if err := requireField(re.Term, path+".term"); err != nil {
			return nil, err
		}
		term, err := parseString(re.Term, path+".term", 1, 200)
		if err != nil {
			return nil, err
		}
		if strings.ContainsAny(term, "\r\n") {
			return nil, fmt.Errorf("%s.term が改行を含む（照合は行単位のため不正）", path)
		}
		e.Term = term

		if err := requireField(re.MaxOccurrences, path+".max_occurrences"); err != nil {
			return nil, err
		}
		if e.MaxOccurrences, err = parseInt(re.MaxOccurrences, path+".max_occurrences"); err != nil {
			return nil, err
		}
		if e.MaxOccurrences < 1 {
			return nil, fmt.Errorf("%s.max_occurrences %d は 1 以上でなければならない", path, e.MaxOccurrences)
		}

		if ok, err := optionalField(re.AllowedSurfaces, path+".allowed_surfaces"); err != nil {
			return nil, err
		} else if ok {
			e.AllowedSurfaces, err = parseStringArray(re.AllowedSurfaces, path+".allowed_surfaces", stringArrayRules{
				minItems: 1, minRunes: 1, maxRunes: 50, uniqueItems: true,
			})
			if err != nil {
				return nil, err
			}
		}

		if ok, err := optionalField(re.Reason, path+".reason"); err != nil {
			return nil, err
		} else if ok {
			if e.Reason, err = parseString(re.Reason, path+".reason", 1, 300); err != nil {
				return nil, err
			}
		}

		out = append(out, e)
	}
	return out, nil
}

// checkConsistency は scan/DESIGN.md §5 手順 3（design-claude.md §7.2）の整合性制約を確かめる。
// surfaceDeclared は surface フィールドが存在したか（空配列と欠落を区別するため別に受ける）。
func checkConsistency(m *Manifest, surfaceDeclared bool) error {
	seenID := make(map[string]int, len(m.Rejected))
	for i, r := range m.Rejected {
		if first, dup := seenID[r.ID]; dup {
			return fmt.Errorf("rejected[%d].id %q が rejected[%d].id と重複している", i, r.ID, first)
		}
		seenID[r.ID] = i
	}

	// 例外語が禁止語と正規化後に一致すると、免除と検出が両立しない。
	normalizedTerms := make(map[string]string, len(m.Rejected))
	for _, r := range m.Rejected {
		for _, term := range r.LiteralTerms {
			key := Normalize(term)
			if _, ok := normalizedTerms[key]; !ok {
				normalizedTerms[key] = term
			}
		}
	}

	declaredSurfaces := make(map[string]struct{}, len(m.Surface))
	for _, s := range m.Surface {
		declaredSurfaces[s] = struct{}{}
	}

	for i, e := range m.VisibleExceptions {
		if original, ok := normalizedTerms[Normalize(e.Term)]; ok {
			return fmt.Errorf("visible_exceptions[%d].term %q が literal_terms %q と正規化後に一致する", i, e.Term, original)
		}
		for _, s := range e.AllowedSurfaces {
			if !surfaceDeclared {
				return fmt.Errorf("visible_exceptions[%d].allowed_surfaces を使うには surface の宣言が必要", i)
			}
			if _, ok := declaredSurfaces[s]; !ok {
				return fmt.Errorf("visible_exceptions[%d].allowed_surfaces %q が surface に含まれない", i, s)
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// 照合（scan/DESIGN.md §6）
// ---------------------------------------------------------------------------

// interval は正規化後の行における rune 単位の半開区間 [start, end)。
type interval struct{ start, end int }

// overlaps は [start, end) がマスク区間のいずれかと 1 rune でも重なるかを返す。
func overlaps(mask []interval, start, end int) bool {
	for _, iv := range mask {
		if start < iv.end && iv.start < end {
			return true
		}
	}
	return false
}

// indexRunes は hay の from 以降から needle の最初の出現位置（rune オフセット）を返す。
// 見つからなければ -1。想定入力規模では素朴な走査で十分（scan/DESIGN.md §6.5）。
func indexRunes(hay, needle []rune, from int) int {
	if len(needle) == 0 {
		return -1
	}
	for i := from; i+len(needle) <= len(hay); i++ {
		found := true
		for j := range needle {
			if hay[i+j] != needle[j] {
				found = false
				break
			}
		}
		if found {
			return i
		}
	}
	return -1
}

// splitLines は初稿を行へ分ける。行番号は 1 始まり、CRLF の \r は落とす（§6.1）。
func splitLines(draft string) []string {
	lines := strings.Split(draft, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimSuffix(line, "\r")
	}
	return lines
}

// excerptOf は原文行から hits[].excerpt を作る（§2.3）。
// 前後の空白（全角スペースを含む Unicode White_Space）を落とし、上限を超える分は切り詰める。
func excerptOf(line string) string {
	trimmed := strings.TrimSpace(line)
	runes := []rune(trimmed)
	if len(runes) > excerptMaxRunes {
		return string(runes[:excerptMaxRunes]) + excerptEllipsis
	}
	return trimmed
}

// literalTarget は照合する禁止語 1 つ。正規化後に同形の語は 1 つに畳み、
// 原表記は処理順で最初のものを報告する（§6.3）。
type literalTarget struct {
	original   string
	normalized []rune
}

// literalTargets は manifest 全体の literal_terms を処理順（rejected の順 → 各 literal_terms の順）に
// 並べ、正規化後に同形のものを畳んで返す。
func literalTargets(m *Manifest) []literalTarget {
	targets := make([]literalTarget, 0)
	seen := make(map[string]struct{})
	for _, r := range m.Rejected {
		for _, term := range r.LiteralTerms {
			key := Normalize(term)
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			targets = append(targets, literalTarget{original: term, normalized: []rune(key)})
		}
	}
	return targets
}

// located は整列前の hit。行番号・行内オフセット・検出順を持つ（§6.4）。
type located struct {
	hit    Hit
	line   int
	offset int
	seq    int
}

// Scan は初稿を manifest に照らして検査する。
// 先に visible_exceptions の回数判定とマスクを行い（§6.2）、そのあと literal_terms を照合する（§6.3）。
func Scan(m *Manifest, draft string) Result {
	lines := splitLines(draft)
	normalized := make([][]rune, len(lines))
	for i, line := range lines {
		normalized[i] = []rune(Normalize(line))
	}
	masks := make([][]interval, len(lines))

	// excerpt は同じ行で何度も使い回すため、行ごとに一度だけ作る。
	excerpts := make([]string, len(lines))
	excerptReady := make([]bool, len(lines))

	var found []located
	record := func(term string, lineIdx, offset int) {
		if !excerptReady[lineIdx] {
			excerpts[lineIdx] = excerptOf(lines[lineIdx])
			excerptReady[lineIdx] = true
		}
		found = append(found, located{
			hit:    Hit{Term: term, Line: lineIdx + 1, Excerpt: excerpts[lineIdx]},
			line:   lineIdx + 1,
			offset: offset,
			seq:    len(found),
		})
	}

	// 手順1: 例外語の回数判定とマスク。免除・超過を問わず出現領域を記録する。
	for _, e := range m.VisibleExceptions {
		term := []rune(Normalize(e.Term))
		occurrences := 0
		for li := range normalized {
			for pos := 0; ; {
				i := indexRunes(normalized[li], term, pos)
				if i < 0 {
					break
				}
				end := i + len(term)
				if overlaps(masks[li], i, end) {
					// 先行する例外語のマスクと重なる出現は数えない（記載順に早い者勝ち）。
					pos = i + 1
					continue
				}
				occurrences++
				if occurrences > e.MaxOccurrences {
					record(e.Term, li, i)
				}
				masks[li] = append(masks[li], interval{start: i, end: end})
				pos = end
			}
		}
	}

	// 手順2: 禁止語の照合。マスクと 1 rune でも重なる出現は無視する。
	for _, target := range literalTargets(m) {
		for li := range normalized {
			for pos := 0; ; {
				i := indexRunes(normalized[li], target.normalized, pos)
				if i < 0 {
					break
				}
				end := i + len(target.normalized)
				if overlaps(masks[li], i, end) {
					pos = i + 1
					continue
				}
				record(target.original, li, i)
				pos = end
			}
		}
	}

	sort.SliceStable(found, func(a, b int) bool {
		switch {
		case found[a].line != found[b].line:
			return found[a].line < found[b].line
		case found[a].offset != found[b].offset:
			return found[a].offset < found[b].offset
		default:
			return found[a].seq < found[b].seq
		}
	})

	hits := make([]Hit, 0, len(found))
	for _, f := range found {
		hits = append(hits, f.hit)
	}
	return Result{Pass: len(hits) == 0, Hits: hits}
}
