# Media-specific requirements

Supplement to `SKILL.md`. Read it before writing the Clean Brief (step 2) when the deliverable is an
image generation prompt, a video generation prompt, or UI copy and labels. The text section is short
and applies to any written surface.

Common rule for every medium: **report as checked only what was actually inspected.** L1 and L2 see
the draft text. Nobody in this pipeline sees rendered pixels, frames, or audio unless you opened the
real file. Say so in any report.

## Text

Applies to copy, headlines, CTAs, articles, posts, dialogue, narration, subtitles, and summaries.

- Inspection covers every surface: headline, opening, body, CTA, footnotes. Label each one in the
  draft so L2 can check where the attention goes.
- Do not fall back on explaining an absence (「廃止しました」「現在はありません」, "no longer
  available"). The current offer is the subject.
- Do not put the change history on the finished surface unless the user asked for a diff or a change
  report. In that case the skill does not apply to that report.

## Image generation prompts

- Describe, in the affirmative, the subject, shape, placement, material, light, and contact
  relationships that are needed now.
- Do not re-list rejected objects as a long negative prompt. A negative prompt that names the old plan
  is a Literal leak and keeps the model's attention on it.
- Check that in-image text, labels, signage, packaging, price tags, and background props carry no
  trace of the old plan.
- Watch for residue shapes: the removed object's outline, fragments, shadow, reflection, its container,
  plate, handle, or an empty stand or placeholder where it used to be.
- Declare an image surface in the manifest (`image_prompt` or similar) so L2 applies the Visual check.
- If you have not looked at the rendered image, do not report pixel-level residue as inspected.

## Video generation prompts

- Define the subject, causality, actions, camera, editing, sound, on-screen text, and the final frame
  that are needed now.
- Do not keep old-plan events in the script as things that "do not happen". An event that is named as
  not happening is still on the surface.
- Check the audio track on its own: alarms, failure sounds, or lines of dialogue from the old plan can
  survive there when the visuals are clean.
- Check the final frame separately. It is the last thing the viewer sees and the most common place for
  an old title card or product to linger.
- Declare a video surface in the manifest (`video_prompt`, `storyboard`, or similar) so L2 applies the
  Visual check.
- If you have not watched the rendered video, do not report frame or audio residue as inspected.

## UI copy and labels

- Do not leave a hidden feature's name as an empty field, a disabled label, a placeholder, or a tooltip.
  Remove the element or give it its current purpose.
- Change-history screens and administrator audit screens are outside this skill: they exist to name
  what changed.
- Never remove errors, safety notices, or status messages the user needs. These rank above the
  current-state preference (priority 1 in `SKILL.md`).
- Check every locale and every string file the surface is built from, not just the one in the
  conversation.
- When UI copy lives in a repository, the L1 CLI may run continuously (MAY): as a textlint or ESLint
  custom rule fed with the same `literal_terms`, or from a Claude Code `PostToolUse` hook (matcher
  `Write|Edit`) that rejects a write when `pink-elephant-scan` exits 1. L1 then applies without going
  through the model's judgment. This is an option the user chooses, not a default of this skill.
