import { defineConfig } from 'vitepress'

const siteURL = 'https://local-agent.dev'
const repositoryURL = 'https://github.com/abdul-hamid-achik/local-agent'
const socialImageURL = `${siteURL}/social-card.png`
const defaultDescription =
  'Run a local-first coding agent in your terminal with Ollama, a Charm TUI, host-governed tools, MCP integrations, sessions, and durable goals.'

const structuredData = JSON.stringify({
  '@context': 'https://schema.org',
  '@graph': [
    {
      '@type': 'WebSite',
      '@id': `${siteURL}/#website`,
      url: `${siteURL}/`,
      name: 'local-agent',
      description: defaultDescription,
      inLanguage: 'en-US',
      mainEntity: { '@id': `${siteURL}/#software` },
    },
    {
      '@type': 'SoftwareApplication',
      '@id': `${siteURL}/#software`,
      name: 'local-agent',
      url: `${siteURL}/`,
      isPartOf: { '@id': `${siteURL}/#website` },
      applicationCategory: 'DeveloperApplication',
      applicationSubCategory: 'AI coding agent',
      operatingSystem: ['macOS', 'Linux'],
      description:
        'A local-first terminal coding agent with Ollama model routing, host-governed tools, MCP integrations, sessions, and durable goals.',
      softwareRequirements: 'Ollama',
      sameAs: repositoryURL,
      downloadUrl: `${repositoryURL}/releases`,
      license: `${repositoryURL}/blob/main/LICENSE`,
      author: {
        '@type': 'Person',
        name: 'Abdul Hamid Achik',
      },
      isAccessibleForFree: true,
      offers: {
        '@type': 'Offer',
        price: 0,
        priceCurrency: 'USD',
      },
      featureList: [
        'Live Ollama model discovery and routing',
        'Approval-gated repository and shell tools by default',
        'Model Context Protocol integrations',
        'Durable local sessions and reviewed goals',
      ],
    },
  ],
})

function routeFromPage(page: string): string {
  const clean = page
    .replaceAll('\\', '/')
    .replace(/(^|\/)index\.md$/, '$1')
    .replace(/\.md$/, '')
    .replace(/^\/+|\/+$/g, '')

  return clean === '' ? '/' : `/${clean}`
}

function socialTitle(pageTitle: string): string {
  return pageTitle === 'local-agent' ? pageTitle : `local-agent — ${pageTitle}`
}

function stripBrand(pageTitle: string): string {
  return pageTitle.replace(/^local-agent\s+—\s+/i, '')
}

export default defineConfig({
  title: 'local-agent',
  titleTemplate: 'local-agent — :title',
  description: defaultDescription,
  lang: 'en-US',
  cleanUrls: true,
  lastUpdated: true,
  sitemap: {
    hostname: siteURL,
  },
  markdown: {
    // Code blocks intentionally stay terminal-dark in both site themes.
    theme: {
      light: 'github-dark',
      dark: 'github-dark',
    },
  },

  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/logo.svg' }],
    ['meta', { name: 'theme-color', content: '#151916' }],
    ['meta', { name: 'color-scheme', content: 'dark light' }],
    ['meta', { name: 'author', content: 'Abdul Hamid Achik' }],
  ],

  transformHead(context) {
    if (!context.page) return []

    const route = routeFromPage(context.page)
    if (route === '/404') {
      return [['meta', { name: 'robots', content: 'noindex, nofollow' }]]
    }

    const pageTitle =
      String(context.pageData.frontmatter.title || context.title || 'Documentation')
    const description =
      String(context.pageData.frontmatter.description || context.description || defaultDescription)
    const canonicalURL = new URL(route, `${siteURL}/`).href
    const title = socialTitle(stripBrand(pageTitle))
    const head = [
      ['meta', { name: 'robots', content: 'index, follow' }],
      ['link', { rel: 'canonical', href: canonicalURL }],
      ['meta', { property: 'og:type', content: 'website' }],
      ['meta', { property: 'og:site_name', content: 'local-agent' }],
      ['meta', { property: 'og:locale', content: 'en_US' }],
      ['meta', { property: 'og:title', content: title }],
      ['meta', { property: 'og:description', content: description }],
      ['meta', { property: 'og:url', content: canonicalURL }],
      ['meta', { property: 'og:image', content: socialImageURL }],
      ['meta', { property: 'og:image:type', content: 'image/png' }],
      ['meta', { property: 'og:image:width', content: '1200' }],
      ['meta', { property: 'og:image:height', content: '630' }],
      [
        'meta',
        {
          property: 'og:image:alt',
          content: 'local-agent local-first coding agent for Ollama with Normal, Plan, and Auto modes',
        },
      ],
      ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
      ['meta', { name: 'twitter:title', content: title }],
      ['meta', { name: 'twitter:description', content: description }],
      ['meta', { name: 'twitter:image', content: socialImageURL }],
      [
        'meta',
        {
          name: 'twitter:image:alt',
          content: 'local-agent local-first coding agent for Ollama with Normal, Plan, and Auto modes',
        },
      ],
    ]

    if (route === '/') {
      head.push(['script', { type: 'application/ld+json' }, structuredData])
    }

    return head
  },

  themeConfig: {
    logo: '/logo.svg',
    siteTitle: 'local-agent',
    search: {
      provider: 'local',
    },
    nav: [
      { text: 'Get Started', link: '/getting-started' },
      {
        text: 'Use local-agent',
        items: [
          { text: 'Ollama Models', link: '/ollama-models' },
          { text: 'Modes and Goals', link: '/modes-and-goals' },
          { text: 'Sessions and Memory', link: '/sessions-and-memory' },
          { text: 'Legacy Data Recovery', link: '/legacy-data-migration' },
          { text: 'Configuration', link: '/configuration' },
          { text: 'Command Reference', link: '/reference' },
        ],
      },
      {
        text: 'Trust and Extend',
        items: [
          { text: 'Safety and Privacy', link: '/safety' },
          { text: 'MCP Integrations', link: '/mcp' },
          { text: 'Testing', link: '/testing' },
          { text: 'Architecture', link: '/architecture' },
          { text: 'Ecosystem', link: '/ecosystem' },
        ],
      },
    ],
    sidebar: [
      {
        text: 'Start',
        items: [
          { text: 'Overview', link: '/' },
          { text: 'Getting Started', link: '/getting-started' },
          { text: 'Ollama Models', link: '/ollama-models' },
        ],
      },
      {
        text: 'Work',
        items: [
          { text: 'Modes and Goals', link: '/modes-and-goals' },
          { text: 'Sessions and Memory', link: '/sessions-and-memory' },
          { text: 'Legacy Data Recovery', link: '/legacy-data-migration' },
          { text: 'Configuration', link: '/configuration' },
          { text: 'Command Reference', link: '/reference' },
        ],
      },
      {
        text: 'Integrate',
        items: [
          { text: 'MCP Integrations', link: '/mcp' },
          { text: 'Ecosystem', link: '/ecosystem' },
        ],
      },
      {
        text: 'Trust',
        items: [
          { text: 'Safety and Privacy', link: '/safety' },
          { text: 'Testing', link: '/testing' },
          { text: 'Architecture', link: '/architecture' },
        ],
      },
    ],
    socialLinks: [{ icon: 'github', link: repositoryURL }],
    editLink: {
      pattern: `${repositoryURL}/edit/main/docs/:path`,
      text: 'Edit this page on GitHub',
    },
    outline: {
      level: [2, 3],
      label: 'On this page',
    },
    docFooter: {
      prev: 'Previous',
      next: 'Next',
    },
    footer: {
      message: 'Local-first by default. Approval-gated by design.',
      copyright: 'Released under the MIT License.',
    },
  },
})
