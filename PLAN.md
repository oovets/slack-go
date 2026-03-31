# Slack GUI Professionalization Plan

## Goal
Build a clean, fast, professional Slack-like desktop client without bloat.

Success criteria:
- Feels polished and consistent in daily use.
- Keyboard-first workflows are first-class.
- Startup/runtime stays lightweight.
- Linux packaging is installable and release-ready.

---

## Product Principles
1. Keep it lean: no feature unless it improves daily workflow.
2. Visual consistency over visual novelty.
3. Keyboard + mouse parity for all core actions.
4. Fast feedback for loading, errors, and offline/reconnect states.
5. Ship incrementally with measurable acceptance criteria.

---

## Scope
In scope:
- UI polish, information hierarchy, and interaction design.
- Slack-like UX patterns adapted for Fyne.
- Packaging and release artifacts for Linux.

Out of scope (initially):
- Plugin system.
- Heavy in-app animation framework.
- Full Slack parity for enterprise edge-cases.

---

## Workstreams

## 1) Design System and Theme Foundation
Objective: establish a coherent visual system used everywhere.

Tasks:
- Define semantic color tokens for dark/light modes.
- Define spacing scale (4/8/12/16/24) and text size scale.
- Standardize border radius, separators, and surface contrast.
- Standardize message/action/metadata emphasis levels.
- Reduce default font options; keep advanced font switching in settings.

Deliverables:
- Updated theme constants and mappings.
- UI style reference section in README (short, practical).

Acceptance criteria:
- No ad-hoc colors in core views.
- Sidebar, chat surface, input, and overlays clearly separated.
- Visual rhythm consistent across panes.

---

## 2) Sidebar and Navigation UX
Objective: make channel navigation look professional and feel fast.

Tasks:
- Improve section headers (Channels/DMs/Threads).
- Add clear active row state with subtle accent.
- Improve unread indicators and row hierarchy.
- Keep section collapse behavior, improve affordance.
- Add quick switcher (Ctrl+K) for channel/DM/thread jump.

Deliverables:
- Refined sidebar row rendering.
- Quick switcher dialog with keyboard navigation.

Acceptance criteria:
- Current location is obvious at a glance.
- Unread state is visible without clutter.
- Channel switch can be done with keyboard only.

---

## 3) Message List and Thread UX
Objective: improve readability and thread-oriented workflow.

Tasks:
- Tighten sender grouping and timestamp metadata behavior.
- Add subtle row hover background and better mention highlight.
- Improve thread affordance (reply count/action clarity).
- Ensure quoted/forwarded text has clear visual hierarchy.
- Ensure outgoing/incoming alignment remains clean under wraps.

Deliverables:
- Updated message row rendering and interaction states.

Acceptance criteria:
- Long conversations remain scannable.
- Mentioned messages stand out but are not noisy.
- Thread entry/exit actions are obvious.

---

## 4) Composer and Input Experience
Objective: make writing messages feel modern and reliable.

Tasks:
- Improve composer container styling (surface, border, spacing).
- Add lightweight action row (send hints, optional attach/emoji hooks).
- Clarify Enter vs Shift+Enter behavior in UI.
- Improve reply/thread context banners.
- Preserve current low-latency submit behavior.

Deliverables:
- Refined composer widget layout and microcopy.

Acceptance criteria:
- Composer state (normal/reply/thread) is always obvious.
- Multi-line editing feels stable and predictable.

---

## 5) States, Feedback, and Reliability UX
Objective: professional handling of empty/loading/error/realtime states.

Tasks:
- Add polished empty states (no messages, no channels, no results).
- Add skeleton/loading placeholders where practical.
- Improve realtime reconnect status messaging.
- Add retry actions for common transient failures.
- Ensure state transitions do not cause layout jumps.

Deliverables:
- Reusable status/empty state components.

Acceptance criteria:
- Users always understand what is happening.
- Recoverable failures can be retried in one click/shortcut.

---

## 6) Preferences and Professional Defaults
Objective: keep flexibility but choose strong defaults.

Tasks:
- Keep dark/light, compact mode, font size.
- Move less-common style options under an advanced submenu.
- Add reset-to-defaults action for UI preferences.
- Persist all new UX toggles coherently.

Deliverables:
- Updated View/Settings menu structure.

Acceptance criteria:
- New users get a polished default without tuning.
- Power users can customize without visual breakage.

---

## 7) Packaging and Release Pipeline (Linux)
Objective: deliver installable artifacts that feel production-grade.

Tasks:
- Add packaging assets:
  - app icon set
  - desktop file
  - metadata templates
- Build targets:
  - AppImage (primary quick distribution)
  - .deb and .rpm (native install)
  - Flatpak manifest (phase 2)
- Add release script for versioned outputs and checksums.
- Add install/uninstall notes and troubleshooting docs.

Deliverables:
- `packaging/` directory with scripts and templates.
- Documented release commands in README.

Acceptance criteria:
- Fresh Linux machine can install and launch cleanly.
- App appears in app launcher with proper icon/name/category.
- Release artifacts are reproducible and checksummed.

---

## Execution Phases

## Phase 1: Core visual polish (Week 1)
- Workstream 1 + core parts of 2 and 3.
- Outcome: immediate pro look and readability boost.

## Phase 2: Workflow UX (Week 2)
- Finish 2, 3, and 4.
- Outcome: faster daily usage, Slack-like flow.

## Phase 3: Reliability and defaults (Week 3)
- Workstream 5 and 6.
- Outcome: stable professional behavior in edge states.

## Phase 4: Packaging and release (Week 4)
- Workstream 7.
- Outcome: distributable release artifacts.

---

## Implementation Order (technical)
1. theme.go token cleanup + semantic helper funcs.
2. sidebar row rendering refactor in app.go.
3. message rendering polish in message_view.go/widgets.go.
4. composer refinement in chat_pane.go.
5. quick switcher and command entry in app.go.
6. reusable empty/loading components.
7. packaging assets/scripts + README updates.

---

## Risks and Mitigations
- Risk: Over-design adds complexity.
  - Mitigation: gate each change with measurable UX value.

- Risk: Visual changes regress readability.
  - Mitigation: keep contrast checks and test both themes.

- Risk: Packaging differences across distros.
  - Mitigation: AppImage first, then native package validation matrix.

---

## Validation Checklist
- Build and run on Linux (dark and light modes).
- Keyboard shortcuts still work for all existing flows.
- Message rendering tested for:
  - mentions
  - threads
  - attachments/images
  - long multi-line text
- Realtime reconnect path tested.
- Packaging smoke tests:
  - AppImage launch
  - deb install/uninstall
  - rpm install/uninstall

---

## First Implementation Sprint (start now)
1. Theme token cleanup and consistent surfaces.
2. Sidebar active/unread state polish.
3. Message row hover/background/mention refinement.
4. Composer visual polish.

Definition of done for Sprint 1:
- UI clearly more professional in first impression.
- No performance regression in normal chat navigation.
- No broken existing shortcuts or pane split behavior.
