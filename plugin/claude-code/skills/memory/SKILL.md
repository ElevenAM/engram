---
name: engram-memory
description: "ALWAYS ACTIVE — Persistent memory protocol. You MUST save decisions, conventions, bugs, and discoveries to engram proactively. Do NOT wait for the user to ask."
---

# Engram Persistent Memory — Protocol

You have access to Engram, a persistent memory system that survives across sessions and compactions.
This protocol is MANDATORY and ALWAYS ACTIVE — not something you activate on demand.

## AVAILABLE TOOLS

Core tools are loaded automatically at session start by the UserPromptSubmit hook.
They are available immediately — no manual ToolSearch needed.

- `mem_save`, `mem_search`, `mem_context`, `mem_session_summary`
- `mem_get_observation`, `mem_suggest_topic_key`, `mem_update`
- `mem_session_start`, `mem_session_end`, `mem_save_prompt`

**Fallback**: If tools are unexpectedly unavailable, run `engram setup claude-code`
again and restart Claude Code. Setup repairs the durable MCP config and
permissions allowlist for both current (`mcp__engram__...`) and older
plugin-scoped (`mcp__plugin_engram_engram__...`) server ids.

Admin tools (deferred — use ToolSearch only if needed):
- `mem_stats`, `mem_delete`, `mem_timeline`, `mem_capture_passive`

## PROACTIVE SAVE TRIGGERS (mandatory — do NOT wait for user to ask)

Call `mem_save` IMMEDIATELY and WITHOUT BEING ASKED after any of these:

### After decisions or conventions
- Architecture or design decision made
- Team convention documented or established
- Workflow change agreed upon
- Tool or library choice made with tradeoffs

### After completing work
- Bug fix completed (include root cause)
- Feature implemented with non-obvious approach
- Notion/Jira/GitHub artifact created or updated with significant content
- Configuration change or environment setup done

### After discoveries
- Non-obvious discovery about the codebase
- Gotcha, edge case, or unexpected behavior found
- Pattern established (naming, structure, convention)
- User preference or constraint learned

### After user confirmation or rejection
- User confirms a recommendation you made ("go with that", "let's do that", "sounds good", "agreed", "perfect", or the equivalent in the user's language)
- User rejects an option or approach ("no, better X", "not that one", or the equivalent in the user's language)
- User expresses a preference ("I prefer X over Y", "always do it this way", or the equivalent in the user's language)
- User makes a decision after you presented tradeoffs or options
- A discussion concludes with a clear direction chosen — even if the agent proposed it

### Self-check — ask yourself after EVERY task:
> "Did I or the user just make a decision, confirm a recommendation, express a preference, fix a bug, learn something non-obvious, or establish a convention? If yes, call mem_save NOW."

Format for `mem_save`:
- **title**: Verb + what — short, searchable (e.g. "Fixed N+1 query in UserList", "Chose Zustand over Redux")
- **type**: bugfix | decision | architecture | discovery | pattern | config | preference
- **scope**: `project` (default) | `personal`
- **topic_key** (optional but recommended for evolving topics): stable key like `architecture/auth-model`
- **content**:
  **What**: One sentence — what was done
  **Why**: What motivated it (user request, bug, performance, etc.)
  **Where**: Files or paths affected
  **Learned**: Gotchas, edge cases, things that surprised you (omit if none)

### Topic update rules (mandatory)

- Different topics MUST NOT overwrite each other (example: architecture decision vs bugfix)
- If the same topic evolves, call `mem_save` with the same `topic_key` so memory is updated (upsert) instead of creating a new observation
- If unsure about the key, call `mem_suggest_topic_key` first, then reuse that key consistently
- If you already know the exact ID to fix, use `mem_update`

### COMPACT-SAFE / SAVE-THEN-FORGET (mandatory)

After every successful `mem_save`, the response envelope includes `compact_safe: true`, `id`, and `pointer` (`engram:obs/<id>`):

1. That fact is **durable** — drop the investigative trail from working context; keep the pointer
2. Rehydrate full content later with `mem_get_observation(id)` — do not restate full compact_safe bodies in summaries
3. Before compaction or session close: `mem_save` any remaining durable facts FIRST, then write a **pointer-first** `mem_session_summary`

## WHEN TO SEARCH MEMORY

When the user asks to recall something — any variation of "remember", "recall", "what did we do",
"how did we solve", or the equivalent in the user's language, or references to past work:
1. First call `mem_context` — checks recent session history (fast, cheap)
2. If not found, call `mem_search` with relevant keywords (FTS5 full-text search)
3. If you find a match, use `mem_get_observation` for full untruncated content

Also search memory PROACTIVELY when:
- Starting work on something that might have been done before
- The user mentions a topic you have no context on — check if past sessions covered it
- The user's FIRST message references the project, a feature, or a problem — call `mem_search` with keywords from their message to check for prior work before responding

## SESSION CLOSE PROTOCOL (mandatory)

Before ending a session or saying "done" / "that's it", you MUST:
1. Save any still-unsaved durable facts (decisions, gotchas, patterns, bugs) via `mem_save` — the summary is session metadata shown in recent context, NOT searchable memory, so facts that live only in the summary are unfindable later
2. Call `mem_session_summary` with this structure:

## Durable Coverage
- engram:obs/<id> — [short title of each compact_safe save]
(Pointers only — do NOT restate full content of facts already saved via mem_save)

## Goal
[What we were working on this session]

## Instructions
[User preferences or constraints discovered — skip if none]

## Discoveries
- [Technical findings NOT already under Durable Coverage]

## Accomplished
- [Completed items; prefer pointers for saved facts]

## Next Steps
- [What remains to be done — for the next session]

## Relevant Files
- path/to/file — [what it does or what changed]

This is NOT optional. If you skip this, the next session starts blind.

## PRODUCTION PUSH — IMMORTAL-NOTE AUDIT

Immortal types (architecture, pattern, bugfix, bug) never decay, so nothing ever flags them for review. A production push locks in the current approach — that is the moment to re-verify them:

1. Before pushing/merging to the production (deploy) branch, ask: did this session or branch materially change the architecture or approach?
2. If yes: run `engram prune --immortal --project <project>` and re-read the notes touching the changed area.
3. Update stale notes to match the new reality (`mem_update`), or delete truly obsolete ones (`engram delete <id>`). Never leave an immortal note describing a dead approach.

## AFTER COMPACTION

If you see a message about compaction or context reset:
1. `mem_save` any durable facts from the compacted summary that are not yet pointers (decisions, bugs, gotchas) — get compact_safe certificates
2. Call `mem_session_summary` with a **pointer-first** recap (`## Durable Coverage` with `engram:obs/<id>` lines + working state only)
3. Call `mem_context` — read **Durable this session** and recent pointer lines; rehydrate with `mem_get_observation` as needed
4. Only THEN continue working

Do not skip durable saves. Without them, compaction is lossy amnesia instead of safe eviction.
All core tools are loaded automatically by the hook at session start. If they are unexpectedly missing, rerun `engram setup claude-code` and restart Claude Code.
