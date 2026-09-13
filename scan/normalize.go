package main

import (
	"strings"

	"golang.org/x/text/unicode/norm"
)

// 照合用の正規化で写すカタカナの範囲（scan/DESIGN.md §4 段 3）。
// U+30A1（ァ）〜U+30F6（ヶ）を 0x60 引いて U+3041（ぁ）〜U+3096（ゖ）へ写す。
// 長音符 ー(U+30FC)・中点 ・(U+30FB)・繰返し記号 ヽヾ(U+30FD/30FE)・
// ひらがな対応の無い ヷヸヹヺ(U+30F7〜U+30FA)・゠(U+30A0) はいずれも範囲外で、原表記のまま残る。
const (
	katakanaFirst = 'ァ'  // U+30A1
	katakanaLast  = 'ヶ'  // U+30F6
	kanaShift     = 0x60 // カタカナ → ひらがな の符号位置差
)

// Normalize は照合の両辺（manifest の語と初稿の行）へ等しく適用する正規化である。
// scan/DESIGN.md §4 のとおり NFKC → 小文字化 → カタカナ→ひらがな の順で適用する。
// 形態素解析・表記揺れ展開は行わない（前者は MUST NOT、後者は L3 の責務）。
func Normalize(s string) string {
	s = norm.NFKC.String(s)
	s = strings.ToLower(s)
	return strings.Map(func(r rune) rune {
		if r >= katakanaFirst && r <= katakanaLast {
			return r - kanaShift
		}
		return r
	}, s)
}
