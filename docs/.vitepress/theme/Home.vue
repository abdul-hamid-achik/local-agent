<script setup lang="ts">
import { withBase } from 'vitepress'
import { onBeforeUnmount, onMounted, ref } from 'vue'
import HeroTerminal from './HeroTerminal.vue'

const repositoryURL = 'https://github.com/abdul-hamid-achik/local-agent'

const modes = [
  {
    id: 'build',
    index: '01',
    name: 'build',
    title: 'Interactive work',
    description:
      'Inspect the repository, edit files, run commands, and call MCP tools. Mutating effects require approval by default.',
    scope: 'read · edit · command · MCP',
  },
  {
    id: 'plan',
    index: '02',
    name: 'plan',
    title: 'Read-only analysis',
    description:
      'The host offers only read-only tools and rejects write, edit, command, and MCP attempts regardless of approval. The policy is enforced by the host, not by the prompt.',
    scope: 'read · list · search · fetch · web search',
  },
  {
    id: 'chat',
    index: '03',
    name: 'chat',
    title: 'Questions and research',
    description:
      'The read-only research tools and read-only subagents, without the coding preamble or the todo list: for questions, documentation lookups, and guides. MCP tools stay, each call behind its approval.',
    scope: 'read · search · fetch · web search · subagents · MCP',
  },
]

const modelRows = [
  {
    name: 'qwen3.5:4b-q4_K_M',
    location: 'LOCAL',
    state: 'ready',
    note: 'default — the smallest that passed the agent battery',
  },
  {
    name: 'qwen3.5:4b',
    location: 'LOCAL',
    state: 'warned',
    note: "if Ollama serves it on its MLX runner: it runs, with a warning — that runner ignores the size",
  },
]

// The planner's budget on a 16 GB Mac for the default model, in GiB:
// 11/16 of the memory, less the weights (3,324,173,934 bytes) and 2.5 GiB
// of headroom; the 32768-token KV cache (32 KiB a token) fits in the rest.
const budgetParts = [
  { id: 'weights', label: 'weights', gib: 3.1 },
  { id: 'headroom', label: 'headroom', gib: 2.5 },
  { id: 'kv', label: 'KV at 32k', gib: 1.0 },
  { id: 'free', label: 'left', gib: 4.4 },
]

// The goal, tool and budget illustrations each play once, when they scroll
// into view. Without IntersectionObserver, or with reduced motion, they are
// never armed and show their static look.
const goalRun = ref<HTMLElement | null>(null)
const toolRun = ref<HTMLElement | null>(null)
const budgetBar = ref<HTMLElement | null>(null)
let playObserver: IntersectionObserver | undefined

onMounted(() => {
  const els = [goalRun.value, toolRun.value, budgetBar.value].filter(
    (el): el is HTMLElement => el !== null,
  )
  if (!els.length || typeof IntersectionObserver === 'undefined') return
  if (window.matchMedia('(prefers-reduced-motion: reduce)').matches) return
  for (const el of els) el.classList.add('is-armed')
  playObserver = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue
        entry.target.classList.add('is-playing')
        playObserver?.unobserve(entry.target)
      }
    },
    { threshold: 0.4 },
  )
  for (const el of els) playObserver.observe(el)
})

onBeforeUnmount(() => playObserver?.disconnect())

const ecosystemGroups = [
  {
    label: 'Interface and testing',
    names: 'Tuimark · Glyphrun',
    note: 'The terminal UI engine and terminal contract tests',
  },
  {
    label: 'Context and state',
    names: 'Vecgrep · TinyVault · file.cheap',
    note: 'Related search, storage, and secrets tools',
  },
  {
    label: 'Evidence',
    names: 'Glyphrun · Cairntrace · Vidtrace',
    note: 'Terminal, browser, and recording evidence',
  },
  {
    label: 'Operations',
    names: 'Monitor · Bob',
    note: 'Related observation and repository tooling',
  },
]
</script>

<template>
  <main class="la-home">
    <section class="la-hero" aria-labelledby="home-title">
      <div class="la-hero-copy la-enter la-enter-first">
        <p class="la-eyebrow">
          <span class="la-status-dot" aria-hidden="true"></span>
          Alpha · local-first · Ollama
        </p>

        <h1 id="home-title">
          A local coding agent
          <span>with explicit authority.</span>
        </h1>

        <p class="la-lead">
          local-agent is an experimental terminal coding agent for Ollama, built on Bun and
          rendered with
          <a href="https://tuimark.dev" target="_blank" rel="noopener noreferrer">Tuimark</a>.
          Inspect repositories, edit files, run commands, and call MCP tools in build; switch to
          plan and the host removes every mutating tool, not just the prompt.
        </p>

        <p class="la-hero-proof">Mutating effects require approval by default.</p>

        <div class="la-actions" aria-label="Get started with local-agent">
          <a class="la-button la-button-primary" :href="withBase('/getting-started')">
            Get started
            <span aria-hidden="true">→</span>
          </a>
          <a class="la-button la-button-secondary" :href="withBase('/safety')">
            Read the safety boundary
          </a>
          <a class="la-button la-button-quiet" :href="repositoryURL">
            GitHub
            <span aria-hidden="true">↗</span>
          </a>
        </div>

        <a class="la-boundary-note" :href="withBase('/safety')">
          <span class="la-boundary-label">Trust boundary</span>
          <span>Local-first validates endpoints. On macOS, commands can run sandboxed; nothing else is.</span>
        </a>
      </div>

      <figure class="la-terminal-figure la-enter la-enter-second">
        <div
          class="la-terminal"
          role="img"
          aria-label="Illustrative local-agent session in the Tuimark client (a demo, not a real run): the model thinks, reads packages/runtime/src/provider.ts, raises its timeout in an edit whose card shows the diff, then a test command waits in an approval dialog, with the composer ready to queue a message"
          aria-describedby="terminal-caption"
        >
          <HeroTerminal />
        </div>
        <figcaption id="terminal-caption">
          A demo turn, drawn from the Tuimark client's own render of it: each tool card says how it
          ended and what an edit changed, and a command waits for your decision before it runs.
        </figcaption>
      </figure>
    </section>

    <dl class="la-invariant-strip" aria-label="local-agent at a glance">
      <div>
        <dt>Inference</dt>
        <dd>Ollama</dd>
      </div>
      <div>
        <dt>Authority</dt>
        <dd>build · plan · chat</dd>
      </div>
      <div>
        <dt>Extensions</dt>
        <dd>MCP integrations</dd>
      </div>
      <div>
        <dt>Platforms</dt>
        <dd>macOS · Linux</dd>
      </div>
    </dl>

    <section class="la-section la-authority" aria-labelledby="authority-title">
      <header class="la-section-heading la-section-heading-sticky">
        <p class="la-kicker">Host-enforced modes</p>
        <h2 id="authority-title">Authority is a mode, not a suggestion.</h2>
        <p>
          The host decides which tool surface exists. Changing modes changes real execution
          policy, not only the wording sent to the model.
        </p>
        <a class="la-text-link" :href="withBase('/modes')">
          Understand the modes <span aria-hidden="true">→</span>
        </a>
      </header>

      <div class="la-mode-ledger">
        <article
          v-for="mode in modes"
          :key="mode.id"
          class="la-mode-row"
          :class="`la-mode-${mode.id}`"
        >
          <span class="la-mode-index" aria-hidden="true">{{ mode.index }}</span>
          <div class="la-mode-name">
            <span>{{ mode.name }}</span>
            <small>{{ mode.title }}</small>
          </div>
          <div class="la-mode-copy">
            <p>{{ mode.description }}</p>
            <code>{{ mode.scope }}</code>
          </div>
        </article>
      </div>
    </section>

    <section class="la-model-section la-section" aria-labelledby="models-title">
      <div class="la-model-copy">
        <p class="la-kicker">Model admission</p>
        <h2 id="models-title">The host admits the model before the first turn.</h2>
        <p>
          Interactive runs check Ollama up front: a missing model or an oversized context stops
          the session with a clear line, not a first-turn surprise.
        </p>
        <ul class="la-check-list">
          <li>Default model qwen3.5:4b-q4_K_M — the smallest that passed the agent battery.</li>
          <li>Any installed model with --model; context planned for your memory and capped at the model's native window.</li>
          <li>A model on Ollama's MLX runner gets a warning: there the size reserves nothing.</li>
          <li>A 15-second admission check before the screen opens.</li>
        </ul>
        <a class="la-text-link" :href="withBase('/ollama-models')">
          Explore Ollama models <span aria-hidden="true">→</span>
        </a>
      </div>

      <div class="la-inventory" aria-label="Illustrative Ollama model inventory">
        <div class="la-inventory-head">
          <div>
            <span>OLLAMA</span>
            <strong>Available models</strong>
          </div>
          <code>/api/tags</code>
        </div>

        <div class="la-inventory-rows">
          <div v-for="model in modelRows" :key="model.name" class="la-model-row">
            <span class="la-model-location">{{ model.location }}</span>
            <div>
              <strong>{{ model.name }}</strong>
              <small>{{ model.note }}</small>
            </div>
            <span class="la-model-state" :class="`la-model-state-${model.state}`">
              {{ model.state }}
            </span>
          </div>
        </div>

        <div class="la-consent-panel">
          <div class="la-consent-heading">
            <span>Context boundary</span>
            <strong>Never past the native window</strong>
          </div>
          <p>
            Without --context, the window is planned from the model's cache cost and the
            machine's memory, up to 32768 tokens, and never past the model's native context. A
            remote Chalupa profile negotiates its own context instead.
          </p>
          <div
            ref="budgetBar"
            class="la-budget"
            role="img"
            aria-label="Illustrative planner budget (a worked example, not a measurement) on a 16 GB Mac for qwen3.5:4b-q4_K_M: of 11 GiB, 3.1 for the weights, 2.5 of headroom, 1.0 for the KV cache at 32768 tokens, 4.4 left"
          >
            <div class="la-budget-head" aria-hidden="true">
              <span>16 GB Mac · qwen3.5:4b-q4_K_M</span>
              <span>11 GiB budget</span>
            </div>
            <div class="la-budget-bar" aria-hidden="true">
              <i
                v-for="(part, index) in budgetParts"
                :key="part.id"
                :class="`la-budget-${part.id}`"
                :style="{ '--share': part.gib / 11, '--step': index }"
              ></i>
            </div>
            <ul class="la-budget-legend" aria-hidden="true">
              <li v-for="part in budgetParts" :key="part.id" :class="`la-budget-${part.id}`">
                <b></b>{{ part.label }} {{ part.gib.toFixed(1) }}
              </li>
            </ul>
          </div>
          <div class="la-consent-actions" aria-hidden="true">
            <span>planned up to 32768</span>
            <strong>native context hard cap</strong>
          </div>
        </div>
      </div>
    </section>

    <section class="la-section la-effects" aria-labelledby="effects-title">
      <header class="la-section-heading la-section-heading-wide">
        <div>
          <p class="la-kicker">Readable effects</p>
          <h2 id="effects-title">See the request, the decision, and the receipt.</h2>
        </div>
        <p>
          By default, repository mutations, commands, and MCP calls require an explicit decision.
          Every decision is a key press in the same transcript you are already reading.
        </p>
      </header>

      <div class="la-effect-board">
        <article class="la-approval-panel" aria-labelledby="approval-panel-title">
          <div class="la-panel-number" aria-hidden="true">01 / APPROVAL</div>
          <h3 id="approval-panel-title">Decisions stay visible.</h3>
          <p>
            When approval is required, the card keeps the tool and its arguments together before
            execution.
          </p>

          <div class="la-request-block">
            <div>
              <span>tool</span>
              <strong>run_command</strong>
            </div>
            <code>$ bun test packages/runtime/…</code>
            <span class="la-request-state">waiting for decision</span>
          </div>

          <dl class="la-policy-list">
            <div>
              <dt>Allow once</dt>
              <dd>y · one request</dd>
            </div>
            <div>
              <dt>Allow for session</dt>
              <dd>a · this session</dd>
            </div>
            <div>
              <dt>Always allow</dt>
              <dd>! · a saved rule for exactly this</dd>
            </div>
            <div>
              <dt>Deny</dt>
              <dd>n or esc · stop this request</dd>
            </div>
          </dl>
        </article>

        <article class="la-goal-panel" aria-labelledby="session-panel-title">
          <div class="la-panel-number" aria-hidden="true">02 / SESSIONS</div>
          <h3 id="session-panel-title">Sessions are opt-in, explicit, and inspectable.</h3>
          <p>
            Nothing is persisted until you point at a session database with --session-db. Then
            resume, list, and inspect stay separate, tool-free operations.
          </p>

          <dl class="la-goal-facts">
            <div>
              <dt>Resume</dt>
              <dd>--resume HANDLE continues a session</dd>
            </div>
            <div>
              <dt>Inspect</dt>
              <dd>--session-info · --recovery-info</dd>
            </div>
            <div>
              <dt>Switch</dt>
              <dd>/sessions picker inside the client</dd>
            </div>
            <div>
              <dt>Safety</dt>
              <dd>interrupted outcomes are never auto-replayed</dd>
            </div>
          </dl>

          <p class="la-goal-note">
            Sessions are experimental and live in a separate SQLite database you choose. Export
            any transcript to markdown with /export.
          </p>
        </article>

        <article class="la-question-panel" aria-labelledby="question-panel-title">
          <div class="la-question-copy">
            <div class="la-panel-number" aria-hidden="true">03 / QUESTIONS</div>
            <h3 id="question-panel-title">When it needs a decision, it asks.</h3>
            <p>
              In the Tuimark client the model can stop and ask one question: a choice, a yes or no,
              or a typed answer. It opens in the transcript, above the composer, and what you were
              typing waits until it closes.
            </p>
            <p class="la-question-rule">
              An answer is never an approval: an edit or command after a "yes" still shows its
              card.
            </p>
          </div>

          <div class="la-question-box" role="img" aria-label="Illustrative question (a demo, not a real run) with three choices, the second one chosen">
            <div aria-hidden="true">
              <div class="la-question-title">Question · choose one</div>
              <p>Which store should the cache use?</p>
              <ol>
                <li><span>1</span> Postgres</li>
                <li class="la-question-sel"><span>2</span> SQLite <small>· one file</small></li>
                <li><span>3</span> Memory only</li>
              </ol>
              <div class="la-question-hint">↑/↓ choose · enter answer · or type its number · esc skip</div>
            </div>
          </div>
        </article>

        <article class="la-question-panel la-goalloop-panel" aria-labelledby="goal-panel-title">
          <div class="la-question-copy">
            <div class="la-panel-number" aria-hidden="true">04 / GOALS</div>
            <h3 id="goal-panel-title">Give it an objective, and it keeps going.</h3>
            <p>
              In the Tuimark client, /goal runs build turns toward an objective until the model
              reports done or blocked, or a limit stops it: 8 turns by default, 30 minutes, 200k
              output tokens, or two turns in a row that change nothing. esc or /goal stop ends it.
            </p>
            <p class="la-question-rule">
              Every turn keeps its cards, rules and sandbox. Done is the model's word: the closing
              notice names evidence only for a command that exited 0.
            </p>
          </div>

          <div
            ref="goalRun"
            class="la-question-box la-goal-run"
            role="img"
            aria-label="Illustrative goal (a demo, not a real run): two turns with an edit and a passing test command, ending with the model's done report and its evidence"
          >
            <div aria-hidden="true">
              <div class="la-goal-run-head">
                <span>Local Agent</span>
                <span>qwen3.5:4b</span>
                <span class="la-goal-run-turn">goal 2/8</span>
              </div>
              <ol>
                <li class="la-goal-step la-goal-notice" style="--step: 0">
                  <b>Notice</b> [goal 1/8] Goal: make the fetch tests pass
                </li>
                <li class="la-goal-step" style="--step: 1"><b>Edit</b> ✓ src/fetch.ts</li>
                <li class="la-goal-step la-goal-notice" style="--step: 2">
                  <b>Notice</b> [goal 2/8] Continue toward the goal. Last status: the retry is in.
                </li>
                <li class="la-goal-step" style="--step: 3">
                  <b>Command</b> ✓ bun test src/fetch.test.ts
                </li>
                <li class="la-goal-step" style="--step: 4">
                  <b>Goal status</b> done · the fetch tests pass
                </li>
                <li class="la-goal-step la-goal-notice la-goal-end" style="--step: 5">
                  <b>Notice</b> Goal reported done — evidence: bun test src/fetch.test.ts exited 0
                </li>
              </ol>
              <div class="la-question-hint">/goal shows it · /goal stop or esc ends it</div>
            </div>
          </div>
        </article>

        <article class="la-question-panel la-goalloop-panel la-tools-panel" aria-labelledby="tools-panel-title">
          <div class="la-question-copy">
            <div class="la-panel-number" aria-hidden="true">05 / TOOLS</div>
            <h3 id="tools-panel-title">One gateway, every tool, one card each.</h3>
            <p>
              Point your MCP config at a gateway such as mcphub and the model finds the tool it
              needs: resolve_tool routes a goal to a tool, call_tool runs it. Plan mode offers no MCP
              at all.
            </p>
            <p class="la-question-rule">
              The card names the tool behind the gateway, never call_tool, so a session grant (a)
              covers that one tool alone. A result the server does not mark as an error still reads
              unverified.
            </p>
          </div>

          <div
            ref="toolRun"
            class="la-question-box la-goal-run"
            role="img"
            aria-label="Illustrative MCP call through a gateway (a demo, not a real run): the model resolves a validate tool, its card names the downstream tool, and the result reads unverified"
          >
            <div aria-hidden="true">
              <div class="la-goal-run-head">
                <span>Local Agent</span>
                <span>qwen3.5:4b</span>
                <span class="la-goal-run-turn">build</span>
              </div>
              <ol>
                <li class="la-goal-step" style="--step: 0">
                  <b>You</b> validate ui/app.tui
                </li>
                <li class="la-goal-step" style="--step: 1">
                  <b>MCP</b> ✓ resolve_tool → tuimark__validate · confident
                </li>
                <li class="la-goal-step la-goal-notice" style="--step: 2">
                  <b>Approve</b> Call validate from server tuimark? Run by the gateway mcphub
                </li>
                <li class="la-goal-step" style="--step: 3">
                  <b>MCP</b> tuimark validate · ok · 0 diagnostics
                </li>
                <li class="la-goal-step la-goal-notice la-goal-end" style="--step: 4">
                  <b>Result</b> unverified — the server answered; the check is yours
                </li>
              </ol>
              <div class="la-question-hint">y allow · a session · ! always · n deny · esc deny</div>
            </div>
          </div>
        </article>
      </div>
    </section>

    <section class="la-safety-band" aria-labelledby="safety-title">
      <div class="la-safety-intro">
        <p class="la-kicker la-kicker-on-dark">Inspect the boundary</p>
        <h2 id="safety-title">Local-first is precise, not absolute.</h2>
      </div>

      <ul class="la-safety-list">
        <li>
          <span>01</span>
          <p>
            Ollama runs on your machine by default, on loopback. A remote Chalupa provider
            requires an explicit environment opt-out from local-only.
          </p>
        </li>
        <li>
          <span>02</span>
          <p>
            plan mode removes the mutating tools at the host: write, edit, command, and MCP calls
            are denied no matter what the model asks for.
          </p>
        </li>
        <li>
          <span>03</span>
          <p>
            Commands run exactly as written — there is no shell — with your permissions, in your
            workspace, behind an approval card.
          </p>
        </li>
      </ul>

      <a class="la-button la-button-on-dark" :href="withBase('/safety')">
        Read the full safety model <span aria-hidden="true">→</span>
      </a>
    </section>

    <section class="la-section la-ecosystem" aria-labelledby="ecosystem-title">
      <header class="la-section-heading la-section-heading-wide">
        <div>
          <p class="la-kicker">Focused local tools</p>
          <h2 id="ecosystem-title">One host. A wider local-first toolchain.</h2>
        </div>
        <p>
          Connected integrations, test infrastructure, and related projects keep separate
          ownership boundaries. MCP-capable does not mean bundled or silently trusted.
        </p>
      </header>

      <div class="la-ecosystem-ledger">
        <div v-for="(group, index) in ecosystemGroups" :key="group.label" class="la-ecosystem-row">
          <span class="la-ecosystem-index" aria-hidden="true">0{{ index + 1 }}</span>
          <span class="la-ecosystem-label">{{ group.label }}</span>
          <strong>{{ group.names }}</strong>
          <small>{{ group.note }}</small>
        </div>
      </div>

      <a class="la-button la-button-secondary" :href="withBase('/ecosystem')">
        Explore the ecosystem <span aria-hidden="true">→</span>
      </a>
    </section>

    <section class="la-final-cta" aria-labelledby="start-title">
      <div>
        <p class="la-kicker">Start from source</p>
        <h2 id="start-title">Bring the agent to where your code already lives.</h2>
        <p>Clone the repository, connect Ollama, and begin with a read-only request in plan mode.</p>
      </div>

      <div class="la-install-block">
        <span>Run with Bun</span>
        <code>bun install --frozen-lockfile && bun run dev</code>
        <div class="la-actions">
          <a class="la-button la-button-primary" :href="withBase('/getting-started')">
            Follow the quickstart <span aria-hidden="true">→</span>
          </a>
          <a class="la-button la-button-quiet" :href="repositoryURL">Review the source</a>
        </div>
      </div>
    </section>
  </main>
</template>
