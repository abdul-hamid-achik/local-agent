<script setup lang="ts">
import { withBase } from 'vitepress'

const repositoryURL = 'https://github.com/abdul-hamid-achik/local-agent'

const modes = [
  {
    id: 'normal',
    index: '01',
    name: 'NORMAL',
    title: 'Interactive work',
    description:
      'Inspect the repository, edit files, run commands, use memory, and call MCP tools. Mutating effects require approval by default.',
    scope: 'read · edit · shell · memory · MCP',
  },
  {
    id: 'plan',
    index: '02',
    name: 'PLAN',
    title: 'Read-only analysis',
    description:
      'Explore, search, list, compare, and recall. The host removes mutation tools and rejects mutation attempts.',
    scope: 'read · search · list · diff · recall',
  },
  {
    id: 'auto',
    index: '03',
    name: 'AUTO',
    title: 'Reviewed durable goal',
    description:
      'Run a bounded goal in the foreground with explicit budgets, permits, receipts, and recovery. AUTO is not YOLO.',
    scope: 'reviewed goal · same approval policy',
  },
]

const modelRows = [
  {
    name: 'qwen3.5:4b',
    location: 'LOCAL',
    state: 'ready',
    note: 'eligible for automatic routing',
  },
  {
    name: 'qwen3.5:2b',
    location: 'LOCAL',
    state: 'ready',
    note: 'compact installed tier',
  },
  {
    name: 'Ollama Cloud alias',
    location: 'CLOUD',
    state: 'consent',
    note: 'manual selection only',
  },
]

const ecosystemGroups = [
  {
    label: 'Goals and MCP',
    names: 'Cortex · MCPHub',
    note: 'Optional advisor and recommended gateway',
  },
  {
    label: 'Context and state',
    names: 'Vecgrep · VecLite · TinyVault · file.cheap',
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
          local-agent is an alpha terminal coding agent for Ollama. Inspect repositories, edit
          files, run commands, and call MCP tools in NORMAL; enforce read-only work in PLAN; or
          review a bounded durable goal in AUTO.
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
          aria-label="Illustrative local-agent terminal session showing a local Ollama model, completed read tools, and a shell command waiting for approval"
          aria-describedby="terminal-caption"
        >
          <div aria-hidden="true">
            <div class="la-terminal-bar">
              <span>local-agent / workspace</span>
              <span class="la-terminal-mode">NORMAL</span>
            </div>

            <div class="la-terminal-context">
              <div>
                <span>model</span>
                <strong>qwen3.5:4b</strong>
                <small>local</small>
              </div>
              <div>
                <span>session</span>
                <strong>repository review</strong>
                <small>active</small>
              </div>
            </div>

            <div class="la-terminal-body">
              <div class="la-terminal-message">
                <span class="la-terminal-speaker">assistant</span>
                <p>I’ll inspect the model boundary, then run the focused checks.</p>
              </div>

              <div class="la-tool-line">
                <span class="la-tool-index">01</span>
                <span><strong>Read</strong> internal/llm/manager.go</span>
                <small>[done]</small>
              </div>
              <div class="la-tool-line">
                <span class="la-tool-index">02</span>
                <span><strong>Find</strong> cloud consent policy</span>
                <small>[done]</small>
              </div>

              <div class="la-approval-card">
                <div class="la-approval-head">
                  <span>Permission required</span>
                  <strong>bash</strong>
                </div>
                <code>go test ./internal/llm/... -race</code>
                <div class="la-approval-options">
                  <span>[y] once</span>
                  <span>[a] always</span>
                  <span>[n] deny</span>
                </div>
              </div>

              <div class="la-composer-line">
                <span>&gt;</span>
                <span class="la-composer-placeholder">Ask, @mention files, or type /help</span>
                <span class="la-cursor" aria-hidden="true"></span>
              </div>
            </div>

            <div class="la-terminal-footer">
              <span>workspace scoped</span>
              <span>approval pending</span>
            </div>
          </div>
        </div>
        <figcaption id="terminal-caption">
          An illustrative NORMAL session. Tool effects stay visible before they run.
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
        <dd>NORMAL · PLAN · AUTO</dd>
      </div>
      <div>
        <dt>Extensions</dt>
        <dd>MCP tools</dd>
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
        <a class="la-text-link" :href="withBase('/modes-and-goals')">
          Understand modes and goals <span aria-hidden="true">→</span>
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
        <p class="la-kicker">Live Ollama inventory</p>
        <h2 id="models-title">The model list comes from the runtime.</h2>
        <p>
          local-agent reads Ollama’s authoritative inventory, groups local and Cloud entries, and
          routes automatically only among admitted local models.
        </p>
        <ul class="la-check-list">
          <li>See installed weights and Cloud aliases in one picker.</li>
          <li>Inspect capabilities and runtime details before switching.</li>
          <li>Pull a model through a cancellable form.</li>
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
            <span>Cloud boundary</span>
            <strong>Conversation-only consent</strong>
          </div>
          <p>
            A Cloud model crosses the local inference boundary. With local-only enabled, manual
            selection asks first; automatic routing always stays local.
          </p>
          <div class="la-consent-actions" aria-hidden="true">
            <span>Cancel</span>
            <strong>Allow for conversation</strong>
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
          By default, repository mutations, shell commands, memory changes, and MCP calls require
          an explicit decision. Durable goals use the same policy.
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
              <strong>bash</strong>
            </div>
            <code>go test ./... -race</code>
            <span class="la-request-state">waiting for decision</span>
          </div>

          <dl class="la-policy-list">
            <div>
              <dt>Allow once</dt>
              <dd>one request</dd>
            </div>
            <div>
              <dt>Always allow</dt>
              <dd>persisted by tool name</dd>
            </div>
            <div>
              <dt>Deny</dt>
              <dd>recorded in the session</dd>
            </div>
          </dl>
        </article>

        <article class="la-goal-panel" aria-labelledby="goal-panel-title">
          <div class="la-panel-number" aria-hidden="true">02 / GOAL RUNTIME</div>
          <h3 id="goal-panel-title">AUTO stays bounded and foreground.</h3>
          <p>
            Review the objective and limits before the first turn. Permits, budgets, receipts, and
            recovery remain visible as the goal advances.
          </p>

          <dl class="la-goal-facts">
            <div>
              <dt>Authority</dt>
              <dd>AUTO · same approval policy</dd>
            </div>
            <div>
              <dt>Budgets</dt>
              <dd>time · turns · evaluation tokens</dd>
            </div>
            <div>
              <dt>Persistence</dt>
              <dd>permits · receipts · recovery</dd>
            </div>
            <div>
              <dt>Advisor</dt>
              <dd>Cortex, when reachable</dd>
            </div>
          </dl>

          <p class="la-goal-note">
            Cortex can provide bounded semantic status. local-agent still owns the approvals,
            transcript, budgets, and final stop decision.
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
            Ollama and network MCP endpoints must use local-machine addresses under local-only
            policy.
          </p>
        </li>
        <li>
          <span>02</span>
          <p>Built-in file tools canonicalize paths and remain inside the startup workspace.</p>
        </li>
        <li>
          <span>03</span>
          <p>Approved shell commands and trusted STDIO servers remain separate trust decisions.</p>
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
        <p class="la-kicker">Start in a clean worktree</p>
        <h2 id="start-title">Bring the agent to where your code already lives.</h2>
        <p>Install the current tagged release, connect Ollama, and begin with a read-only request.</p>
      </div>

      <div class="la-install-block">
        <span>Install with Go</span>
        <code>go install github.com/abdul-hamid-achik/local-agent/cmd/local-agent@latest</code>
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
