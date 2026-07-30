---
name: engram-memory-protocol
description: >
  Persistent memory discipline for Engram contributors.
  Trigger: Decisions, bugfixes, discoveries, preferences, or session closure.
license: Apache-2.0
metadata:
  author: gentleman-programming
  version: "1.0"
---

## When to Use

Use this skill when:
- Making architecture or implementation decisions
- Fixing bugs with non-obvious root causes
- Discovering patterns, gotchas, or user preferences
- Closing a session or after compaction

---

## Save Rules

Call `mem_save` immediately after:
- decision
- bugfix
- pattern/discovery
- config/preference changes

Use structured content:
- What
- Why
- Where
- Learned

Use stable `topic_key` for evolving topics.

---

## Search Rules

- On recall requests: `mem_context` first, then `mem_search`.
- Before similar work: run proactive `mem_search`.
- On first message: if user references the project, a feature, or a problem, call `mem_search` with their keywords before responding.

---

## Compact-safe / save-then-forget

After successful `mem_save`, envelope has `compact_safe: true` and `pointer` (`engram:obs/<id>`):
- Drop investigative trail from working context; keep the pointer
- Rehydrate with `mem_get_observation`
- Do not restate full compact_safe bodies in session summaries

## Session Close Rules

Before saying done/listo:
1. `mem_save` remaining durable facts first
2. Call `mem_session_summary` with Durable Coverage (pointers), goal, discoveries, accomplished, next steps, relevant files

After compaction:
1. `mem_save` durable facts that are not yet pointers
2. Pointer-first `mem_session_summary`
3. `mem_context` (Durable this session) then continue
