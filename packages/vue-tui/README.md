# Internal Vue TUI

An agent-independent composition layer over Vue TermUI and OpenTUI. It is
private while the harness exercises its API; it is not a published framework.

`PanelLayout` owns the available terminal space and optionally shows a sidebar
at 100 columns / 16 rows. Primary content and sidebar are slots. Native Box,
Text, Textarea and ScrollBox components are reused rather than reimplemented.

Use camelCase native layout options, including in SFC templates:

```vue
<PanelLayout :sidebar="true">
  <Text>Primary content</Text>
  <Box :flexGrow="1" />
  <Input v-model="draft" autofocus />
  <template #sidebar><Text>Details</Text></template>
</PanelLayout>
```

`Composer` provides a bounded multiline draft editor. Enter submits, Shift+Enter
inserts a newline, and pasted newlines stay in the draft. Its `height` prop is
the outer height in terminal cells, border included (3 by default, which leaves
one editor row; the inline footer budgets for it). The editor shows
`height - 2` rows and long drafts scroll inside the native Textarea.
`submit` is emitted for any draft, including an empty one; the owner decides
what to ignore. `cancel` (Escape) is emitted by a focused editor: a Composer
that is busy when it mounts is not focusable, so its owner handles Escape with
`onKeyDown`. Programmatic `modelValue` changes replace the native buffer, so a
successful submit can clear it without remounting the component. `Approval`
renders the complete preview in a bounded ScrollBox and keeps its deny-first
keyboard footer visible. It emits one decision per prompt: the next `title` or
`preview` re-arms it, and an identical prompt needs a new `key`. The preview is
never truncated (a hidden line could be a line the user approved unseen), so
the owner bounds its size.

Sizes are terminal cells or supported relative dimensions, not browser CSS.
Numeric props need bindings (`:height="3"`). Layout options pass through to
OpenTUI; kebab-case attributes such as `flex-grow` are not normalized by every
upstream primitive. Put layout policy in containers and let children use the
space they receive.

`terminalOptions('fullscreen')` selects the alternate screen;
`terminalOptions('inline')` selects a live footer with captured stdout committed
to native scrollback. Inline content already committed to history is immutable.
`@local-agent/vue-tui/text` exposes safe plain-text formatting without loading
native rendering, so headless clients can use it independently.

Run `bun run tui:dev` at the repository root to edit a model-free `.vue`
playground (fullscreen; `TUI_PLAYGROUND_MODE=inline` previews the inline footer). `bun run typecheck` checks TS 7 source and runs the isolated Vue
template checker; `bun run test` runs Vitest; `bun run test:integration` runs
the application's Glyphrun contracts. `bun run tui:build` builds the playground.

The foundation currently proves layout composition, multiline input, approval,
resize, lifecycle and scrollback. Shared theming, large transcript
virtualization and a standalone package release remain future work. Do not add
application sessions, tool schemas or inference dependencies here.
