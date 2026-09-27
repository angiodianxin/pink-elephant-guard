---
name: pink-elephant-guard
description: Prevent rejected, removed, corrected, or forbidden concepts from resurfacing as the topic of a final deliverable. Use after requests such as 「これは消して」「その案はなし」「入れないで」「そこじゃない」 when revising copy, visuals, dialogue, summaries, labels, UI, or other viewer-facing output; do not use when comparison, compliance, safety, or change history explicitly requires naming the excluded item.
---

# Pink Elephant Guard / ピンクの象ガード

Isolate rejected, removed, corrected, or forbidden elements as change history, and rebuild the
deliverable from the current state only. The goal is not to mask forbidden words. It is to return a
deliverable that stands naturally on the current purpose, with no trace of deletion and no excuses.

This file is the orchestration procedure for L3 (you, the main model). Inspection is delegated:

| Layer | Runs as | Checks |
|---|---|---|
| L1 | `pink-elephant-scan` CLI (deterministic Go binary, zero tokens) | Literal leak: rejected terms re-entering the draft, exception count limits |
| L2 | `pink-elephant-semantic-scan` subagent (Haiku, isolated context) | Semantic / Rationale / Attention / Visual leak |
| L3 | This procedure | Classify, write the manifest and the Clean Brief, generate, regenerate, final judgment |

**PASS is derived only from L1 and L2 results. Never substitute your own inspection for them
and never report a draft as checked when they did not run.**

## When to use

All three must hold:

1. The conversation contains an element that was rejected, removed, corrected, or forbidden.
2. The user is creating or revising viewer- or user-facing output (copy, headline, CTA, dialogue,
   narration, subtitles, image/video prompts, in-image text, UI copy, labels, distributable summaries).
3. The deliverable does not need to explain that history.

Typical trigger phrases (judge by meaning of the whole request, not by string match):
「これは消して」「その案はなし」「入れないで」「そこじゃない」「企画から外れました」「前の設定は使いません」.

## When not to use

Do not hide the excluded element, and do not apply this skill, when the request is:

- A comparison of options, a change log, minutes, or an audit record
- Legal, contractual, advertising disclosure, safety, medical, allergen, risk, or accessibility text
- Incident analysis, root-cause investigation, or recurrence prevention
- A non-use claim the user explicitly wants (keep that term as a visible exception)
- Ordinary new creation with no correction history

This skill never hides facts, failures, risks, or mandatory disclosures for the sake of appearance.

## Priority when judgments conflict

1. Required display for law, safety, contract, or accessibility
2. Content the user explicitly asked to show on the finished surface
3. The current state the user last confirmed
4. Earlier proposals and change history

## Procedure

Flow: `1 → 2 → 3 → 4a → 4b → (5 on FAIL, back to 4a) → final judgment → output`.
Intermediate files live in the session scratchpad, never in the user's repository.

### Step 1: Classify and write the manifest

Sort the conversation into four groups:

- `TARGET_STATE`: what exists after the correction and must be conveyed
- `REJECTED_HISTORY`: withdrawn proposals, deleted items, errors, reasons for the change
- `VISIBLE_EXCEPTIONS`: comparisons, disclosures, or non-use claims that must stay on the surface
- `SURFACE`: what the receiver sees (headline, body, CTA, dialogue, image, UI, narration, ...)

Then write `pink-elephant-manifest.json` in the scratchpad. The machine-readable schema is
`schema/manifest.schema.json` at the repository root; the L1 CLI rejects anything that violates it.

| Field | How to fill it |
|---|---|
| `schema_version` | `1` |
| `target_state` | Summary of the current state (the skeleton of the Clean Brief). Not a transcript |
| `rejected[].id` / `label` | Unique id (`^[a-z0-9][a-z0-9_-]{0,31}$`) and a human-readable name |
| `rejected[].literal_terms` | Every spelling the term could re-enter as. **Expand variants yourself (SHOULD)**: hiragana / katakana / romaji / English / abbreviations / nicknames used in the conversation. L1 only normalizes (NFKC, case, kana), it never expands. L1's recall depends on this list |
| `rejected[].concept` | What the element *is*, so that a paraphrase can be recognized. Do not write why it was rejected |
| `visible_exceptions[]` | `term`, `max_occurrences`, optional `allowed_surfaces` (values from `surface`) and `reason`. A term here must not also appear in any `literal_terms` |
| `surface` | The labels the receiver's surfaces will carry, e.g. `["headline", "body", "cta"]`. **Declare it**: L2's Attention check depends on it. Use an image/video label (`image_prompt`, `video_prompt`, `storyboard`) for generation prompts |

Never put conversation text, customer data, secrets, or full local paths into the manifest.

Ask the user only when advertising the absence itself would change the deliverable and the
conversation does not settle it. Otherwise proceed from the current state.

### Step 2: Write the Clean Brief

Rebuild an internal brief from `TARGET_STATE`, permitted facts, medium, tone, and required structure.

MUST:

- Write in the affirmative: what the deliverable is, not "do not show X".
- Fill the space left by the deletion with content, composition, action, or hierarchy that serves the purpose.
- Do not feed rejection reasons or the original proposal back into the brief.

### Step 3: Generate the draft from the current state

Use the Clean Brief as the sole source. Do not trim words from the previous text; compose again with
the current state as the subject.

Write the draft to a scratchpad file (for example `pink-elephant-draft.txt`). **Start each surface
with its label on the first line, `<surface value>:`, using exactly the values declared in `surface`.**
L2 never guesses surfaces: an unlabeled or mislabeled draft gets no Attention check.

```text
headline: 今月はドリップバッグの詰め合わせ
body: 常温で持ち運べる焙煎違いの3種をそろえました。
cta: 店頭でお受け取りください
```

### Step 4a: L1 literal scan (fail-fast)

```bash
pink-elephant-scan --manifest <scratchpad>/pink-elephant-manifest.json --draft <scratchpad>/pink-elephant-draft.txt
```

The binary is on `PATH`, at `${CLAUDE_PLUGIN_ROOT}/bin/pink-elephant-scan` when installed as a plugin,
or built from `scan/` (see the repository README). Branch on the exit code only:

| exit | Meaning | Action |
|---|---|---|
| 0 | PASS | Go to step 4b |
| 1 | FAIL, `stdout` lists `hits[]` (`term`, `line`, `excerpt`) | Go to step 5. **Do not call L2** |
| 2 | Invocation error | Fix the command |
| 3 | Manifest invalid (details on `stderr`) | Back to step 1: rewrite the manifest |
| 4 | Draft unreadable | Rewrite the draft file and rerun |
| 5 or other | No verdict | Treat as unchecked; report it if it persists |

If L1 cannot run at all (no binary, no Go), do not inspect the draft yourself in its place. Deliver
with an explicit statement that machine checks did not run.

### Step 4b: L2 semantic scan (isolated subagent)

Launch the `pink-elephant-semantic-scan` subagent with the Agent tool. **Pass only the two file
paths. MUST NOT include conversation history, rejection reasons, previous drafts, or your own
summary of what was removed.** Its isolation from the conversation is what makes its verdict
uncontaminated.

```text
manifest: <scratchpad>/pink-elephant-manifest.json
draft: <scratchpad>/pink-elephant-draft.txt
Return only the JSON verdict.
```

Read its JSON (schema: `schema/semantic-scan-output.schema.json`):

- `pass: true`: machine checks are complete. Go to the final judgment.
- `pass: false`: go to step 5 with `findings[]` (`check`, `line`, `quote`, `note`, `rejected_id` or `exception_term`).
- No `pass` key (`{"error": ...}`): no verdict. `draft_unreadable`: rewrite the draft and rerun
  4a → 4b. `manifest_*`: back to step 1.
- `applied_checks` lacks `"attention"` although you labeled surfaces: the labels do not match
  `surface`. Fix the draft or the manifest and rerun. It lacks `"visual"` for an image or video
  prompt: declare a visual surface in `surface` and rerun.

Findings contain no fixes, and you do not ask L2 for any. Regeneration is yours.

### Step 5: Regenerate from the Clean Brief

On FAIL from 4a or 4b, do not delete the flagged words and resubmit. The structure or composition
itself is probably still centered on the old plan. Use the findings to check the Clean Brief, adjust
the brief if needed, regenerate the whole draft from it (step 3), then rerun 4a → 4b.

**Two consecutive FAIL cycles mean the classification is wrong, not the wording.** Return to step 1
and rebuild the manifest: missing `literal_terms` variants, a `concept` that is too narrow or too
broad, or an exception that belongs in `rejected` (or the reverse). One cycle is one pass through
4a/4b that ends in FAIL; a no-verdict result does not count.

### Final judgment (L3)

Only after L1 and L2 both PASS: check that the deliverable stands naturally on its own, the last
acceptance criterion. If, for example, every sentence is "we no longer have ...", adjust the Clean
Brief and regenerate through 4a → 4b again. This judgment can send a passed draft back. It can never
promote an unchecked or failed draft.

Acceptance criteria and who establishes them:

| Criterion | Established by |
|---|---|
| No rejected term outside `VISIBLE_EXCEPTIONS` | L1 (exit 0) |
| No residue through synonyms, negation, or euphemism | L2 `semantic` |
| No deletion reason, change explanation, or emphasis on absence | L2 `rationale` |
| Title, opening, CTA, and conclusion are about the current purpose | L2 `attention` |
| No visual or audio residue in what could be inspected | L2 `visual` (prompt text only) |
| Exceptions stay within their place, purpose, and count | L1 (count), L2 `attention` (placement, intent) |
| Nothing unchecked is reported as checked | `applied_checks` and your report |
| The deliverable reads as the current plan, not as a deletion | L3, this step |

## Media-specific requirements

Read `references/media-requirements.md` (next to this file) before step 2 when the deliverable is an
image generation prompt, a video generation prompt, or UI copy and labels. For plain text, its short
text section applies; the rest is not needed and is not loaded by default.

## Output

- Return a deliverable that stands on `TARGET_STATE` alone. Do not include the manifest, the brief,
  or the findings in it.
- If the user asked for the finished piece only (「完成稿だけ」), attach no report and no description of the steps.
- If a report is requested, cite the L1 output and the L2 JSON. Say which checks were applied
  (`applied_checks`) and what was not inspected, such as actual rendered images, video, or audio.
  Never describe your own reading as verification.
