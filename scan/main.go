package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
)

// 終了コード（scan/DESIGN.md §2.2）。0/1 は検査の判定専用で、
// 検査を実施できなかった失敗は必ず 2/3/4 で返す。
const (
	exitPass     = 0 // PASS（hit 0 件）
	exitFail     = 1 // FAIL（hit 1 件以上）
	exitUsage    = 2 // 引数不正
	exitManifest = 3 // manifest 不正
	exitDraft    = 4 // draft 読取り不可
	// exitInternal は上表に該当しない予期しない失敗（stdout へ書けない等）。
	// §2.2 のとおり呼び出し元は「0/1 以外 = 判定なし」として扱えばよい。
	exitInternal = 5
)

const usageLine = "usage: pink-elephant-scan --manifest <path> --draft <path>"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// fail は §2.4 の書式で stderr へ 1 行出し、そのカテゴリに対応する終了コードを返す。
// category は exit code と 1:1 に対応する（usage=2 / manifest=3 / draft=4）。
func fail(stderr io.Writer, code int, category, format string, args ...any) int {
	fmt.Fprintf(stderr, "pink-elephant-scan: %s error: %s\n", category, fmt.Sprintf(format, args...))
	return code
}

// run は CLI の外形契約そのもの。戻り値がそのまま終了コードになる。
// main() から分離してあるため、プロセスを起動せず検証できる（scan/DESIGN.md §3）。
func run(args []string, stdout, stderr io.Writer) int {
	// flag.ExitOnError は解析エラーで os.Exit(2) を直接呼び独自書式の文面を出すため使えない。
	// ContinueOnError で受け取り、§2.4 の書式へ整形する。
	fs := flag.NewFlagSet("pink-elephant-scan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	manifestPath := fs.String("manifest", "", "pink-elephant-manifest.json のパス")
	draftPath := fs.String("draft", "", "検査対象の初稿ファイル（UTF-8 テキスト）のパス")

	if err := fs.Parse(args); err != nil {
		return fail(stderr, exitUsage, "usage", "%v; %s", err, usageLine)
	}
	switch {
	case fs.NArg() > 0:
		return fail(stderr, exitUsage, "usage", "余分な位置引数 %q; %s", fs.Arg(0), usageLine)
	case *manifestPath == "":
		return fail(stderr, exitUsage, "usage", "--manifest は必須; %s", usageLine)
	case *draftPath == "":
		return fail(stderr, exitUsage, "usage", "--draft は必須; %s", usageLine)
	}

	// manifest の読取り不可も manifest カテゴリの失敗（§5）。
	manifestBytes, err := os.ReadFile(*manifestPath)
	if err != nil {
		return fail(stderr, exitManifest, "manifest", "%v", err)
	}
	manifest, err := ParseManifest(manifestBytes)
	if err != nil {
		return fail(stderr, exitManifest, "manifest", "%v", err)
	}

	draftBytes, err := os.ReadFile(*draftPath)
	if err != nil {
		return fail(stderr, exitDraft, "draft", "%v", err)
	}

	result := Scan(manifest, string(draftBytes))

	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false) // < > & をそのまま出す（§2.3）
	if err := enc.Encode(result); err != nil {
		// 判定はできているが伝達できないため、0/1 は返さない。
		fmt.Fprintf(stderr, "pink-elephant-scan: internal error: %v\n", err)
		return exitInternal
	}
	if result.Pass {
		return exitPass
	}
	return exitFail
}
