import { defineConfig } from 'vitepress'

// The site is served from https://krishna2206.github.io/zefile/, a project page,
// so every asset and link is prefixed with the repository name. Swap `base` to
// '/' the day a custom domain is wired in.
export default defineConfig({
  lang: 'en-US',
  title: 'Zefile',
  description: 'The self-hosted file server that File Browser should have been.',
  base: '/zefile/',
  lastUpdated: true,
  cleanUrls: true,

  // Favicon set generated from the logo. Hrefs include the base (/zefile/);
  // update them if the base path changes.
  head: [
    ['link', { rel: 'icon', type: 'image/x-icon', href: '/zefile/favicon.ico' }],
    ['link', { rel: 'icon', type: 'image/png', sizes: '32x32', href: '/zefile/favicon-32.png' }],
    ['link', { rel: 'apple-touch-icon', href: '/zefile/apple-touch-icon.png' }],
  ],

  // The design documents are internal HTML artifacts kept in docs/design/. They
  // are not part of the built site; VitePress only routes Markdown.
  srcExclude: ['design/**', '**/README.md'],

  themeConfig: {
    logo: '/logo.png',

    nav: [
      { text: 'Guide', link: '/guide/introduction' },
      { text: 'Features', link: '/features/users-and-groups' },
      { text: 'Reference', link: '/reference/api' },
      { text: 'v0.6.0', link: 'https://github.com/krishna2206/zefile/releases' },
    ],

    sidebar: {
      '/': [
        {
          text: 'Guide',
          items: [
            { text: 'Introduction', link: '/guide/introduction' },
            { text: 'Installation', link: '/guide/installation' },
            { text: 'Configuration', link: '/guide/configuration' },
            { text: 'Deployment', link: '/guide/deployment' },
            { text: 'Upgrading', link: '/guide/upgrading' },
          ],
        },
        {
          text: 'Features',
          items: [
            { text: 'Users & groups', link: '/features/users-and-groups' },
            { text: 'Permissions', link: '/features/permissions' },
            { text: 'Sharing', link: '/features/sharing' },
            { text: 'Previews', link: '/features/previews' },
            { text: 'Trash', link: '/features/trash' },
            { text: 'Search', link: '/features/search' },
            { text: 'Audit log', link: '/features/audit-log' },
          ],
        },
        {
          text: 'Reference',
          items: [
            { text: 'API', link: '/reference/api' },
            { text: 'API tokens', link: '/reference/api-tokens' },
            { text: 'Environment variables', link: '/reference/environment' },
          ],
        },
        {
          text: 'Project',
          items: [
            { text: 'Contributing', link: '/contributing' },
          ],
        },
      ],
    },

    socialLinks: [
      { icon: 'github', link: 'https://github.com/krishna2206/zefile' },
    ],

    editLink: {
      pattern: 'https://github.com/krishna2206/zefile/edit/main/docs/:path',
      text: 'Edit this page on GitHub',
    },

    search: { provider: 'local' },

    footer: {
      message: 'Apache-2.0 licensed.',
      copyright: 'Zefile',
    },
  },
})
