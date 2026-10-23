<script setup lang="ts">
import { withBase } from 'vitepress'

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
      'The host offers only read, list, and search, and rejects write, edit, command, and MCP attempts regardless of approval. The policy is enforced by the host, not by the prompt.',
    scope: 'read · list · search',
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
    state: 'ready',
    note: 'any installed Ollama model via --model',
  },
]

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
          <span>Local-first validates endpoints. It is not an operating-system sandbox.</span>
        </a>
      </div>

      <figure class="la-terminal-figure la-enter la-enter-second">
        <div
          class="la-terminal"
          role="img"
          aria-label="Illustrative local-agent terminal session showing a local Ollama model, completed tool cards, and a command waiting for approval"
          aria-describedby="terminal-caption"
        >
          <div aria-hidden="true">
            <div class="la-terminal-bar">
              <span>Local Agent · qwen3.5:4b</span>
              <span class="la-terminal-mode">build</span>
            </div>

            <div class="la-terminal-context">
              <div>
                <span>model</span>
                <strong>qwen3.5:4b</strong>
                <small>local</small>
              </div>
              <div>
                <span>context</span>
                <strong>2.1k/16k</strong>
                <small>████░░░░░░</small>
              </div>
            </div>

            <div class="la-terminal-body">
              <div class="la-terminal-message">
                <span class="la-terminal-speaker">assistant</span>
                <p>I’ll inspect the provider boundary, then run the focused checks.</p>
              </div>

              <div class="la-tool-line">
                <span class="la-tool-index">01</span>
                <span><strong>Tool · read</strong> packages/runtime/src/provider.ts</span>
                <small>[done]</small>
              </div>
              <div class="la-tool-line">
                <span class="la-tool-index">02</span>
                <span><strong>Tool · edit</strong> packages/runtime/src/provider.ts</span>
                <small>[done]</small>
              </div>

              <div class="la-approval-card">
                <div class="la-approval-head">
                  <span>Run command?</span>
                  <strong>Runs exactly as written: there is no shell.</strong>
                </div>
                <code>$ bun test packages/runtime/src/provider.test.ts</code>
                <div class="la-approval-options">
                  <span>[y] once</span>
                  <span>[a] this session</span>
                  <span>[n] deny</span>
                  <span>esc deny</span>
                </div>
              </div>

              <div class="la-composer-line">
                <span>&gt;</span>
                <span class="la-composer-placeholder">Message the model</span>
                <span class="la-cursor" aria-hidden="true"></span>
              </div>
            </div>

            <div class="la-terminal-footer">
              <span>Ready · /help lists keys</span>
              <span>ctrl+p commands · esc cancel</span>
            </div>
          </div>
        </div>
        <figcaption id="terminal-caption">
          An illustrative build session in the Tuimark client. Tool effects stay visible before they run.
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
        <dd>plan · build</dd>
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
          <li>Any installed model with --model; context capped at the model's native window.</li>
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
            --context defaults to 16384 tokens and cannot exceed the model's native context. A
            remote Chalupa profile negotiates its own context instead.
          </p>
          <div class="la-consent-actions" aria-hidden="true">
            <span>16384 default</span>
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
