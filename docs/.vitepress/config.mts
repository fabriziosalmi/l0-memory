import { defineConfig } from 'vitepress'

// https://vitepress.dev/reference/site-config
export default defineConfig({
  title: "l0-memory",
  description: "Long-term memory for AI assistants",
  base: '/l0-memory/',
  head: [
    ['link', { rel: 'icon', href: '/l0-memory/favicon.svg' }],
    ['meta', { name: 'theme-color', content: '#000000' }],
    ['meta', { name: 'apple-mobile-web-app-capable', content: 'yes' }],
    ['meta', { name: 'apple-mobile-web-app-status-bar-style', content: 'black' }]
  ],
  themeConfig: {
    // https://vitepress.dev/reference/default-theme-config
    nav: [
      { text: 'Guide', link: '/guide/getting-started' },
      { text: 'Reference', link: '/reference/cli' },
      { text: 'MCP Tools', link: '/reference/tools' }
    ],

    sidebar: [
      {
        text: 'Introduction',
        items: [
          { text: 'What is l0-memory?', link: '/guide/what-is-l0-memory' },
          { text: 'Getting Started', link: '/guide/getting-started' },
          { text: 'Architecture', link: '/guide/architecture' }
        ]
      },
      {
        text: 'Integration',
        items: [
          { text: 'Claude Code', link: '/guide/integration-claude-code' },
          { text: 'Claude Desktop', link: '/guide/integration-claude-desktop' },
          { text: 'Other MCP Hosts', link: '/guide/integration-other' }
        ]
      },
      {
        text: 'Features',
        items: [
          { text: 'Knowledge Graph', link: '/guide/features-graph' },
          { text: 'Scopes & Freshness', link: '/guide/features-scopes' },
          { text: 'VS Code Extension', link: '/guide/features-vscode' }
        ]
      },
      {
        text: 'Reference',
        items: [
          { text: 'CLI Reference', link: '/reference/cli' },
          { text: 'MCP Tools', link: '/reference/tools' },
          { text: 'Configuration', link: '/reference/config' }
        ]
      },
      {
        text: 'Development',
        items: [
          { text: 'Contributing', link: '/guide/contributing' },
          { text: 'Building from Source', link: '/guide/building' }
        ]
      }
    ],

    socialLinks: [
      { icon: 'github', link: 'https://github.com/fabriziosalmi/l0-memory' }
    ],

    footer: {
      message: 'Crafted with precision for AI assistants.',
      copyright: 'Copyright © 2024-present Fabrizio Salmi'
    },

    search: {
      provider: 'local'
    },

    editLink: {
      pattern: 'https://github.com/fabriziosalmi/l0-memory/edit/main/docs/:path',
      text: 'Edit this page on GitHub'
    }
  }
})
