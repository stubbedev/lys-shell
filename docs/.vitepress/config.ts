import { readdirSync } from 'node:fs'
import { defineConfig } from 'vitepress'

const pages = (dir: string, skip: string[] = []) =>
  readdirSync(new URL(`../config/${dir}`, import.meta.url))
    .filter((f) => f.endsWith('.md'))
    .map((f) => f.slice(0, -3))
    .filter((name) => !skip.includes(name))
    .sort()
    .map((name) => ({ text: name, link: `/config/${dir}${name}` }))

export default defineConfig({
  title: 'lys-shell',
  description: 'A configurable desktop shell for Wayland.',
  base: '/lys-shell/',
  cleanUrls: true,
  lastUpdated: true,

  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/lys-shell/lys-shell.svg' }],
  ],

  markdown: {
    theme: {
      light: 'github-light',
      dark: 'tokyo-night',
    },
  },

  themeConfig: {
    logo: '/lys-shell.svg',

    nav: [
      { text: 'Guide', link: '/guide/getting-started', activeMatch: '^/guide/' },
      { text: 'Config', link: '/config/', activeMatch: '^/config/' },
    ],

    sidebar: {
      '/guide/': [
        {
          text: 'Guide',
          items: [
            {
              text: 'Getting started',
              link: '/guide/getting-started',
              collapsed: false,
              items: [
                { text: 'Arch Linux', link: '/guide/getting-started-arch' },
                { text: 'Debian / Ubuntu', link: '/guide/getting-started-debian' },
                { text: 'Fedora', link: '/guide/getting-started-fedora' },
                { text: 'NixOS', link: '/guide/getting-started-nixos' },
              ],
            },
            { text: 'Editing config', link: '/guide/editing-config' },
            { text: 'Bars and layouts', link: '/guide/bars-and-layouts' },
            { text: 'Themes', link: '/guide/themes' },
            { text: 'Custom styles', link: '/guide/custom-styles' },
            { text: 'Custom icons', link: '/guide/custom-icons' },
            { text: 'Custom modules', link: '/guide/custom-modules' },
            { text: 'Greeter (login screen)', link: '/guide/greeter' },
            { text: 'Secrets (keyring)', link: '/guide/secrets' },
            { text: 'CLI', link: '/guide/cli' },
          ],
        },
        {
          text: 'Also see',
          items: [
            { text: 'Config reference', link: '/config/' },
          ],
        },
      ],

      '/config/': [
        {
          text: 'Config',
          items: [
            { text: 'Overview', link: '/config/' },
            { text: 'Types', link: '/config/types' },
          ],
        },
        {
          text: 'Top-level',
          items: pages('', ['index', 'types']),
        },
        {
          text: 'Modules',
          items: pages('modules/'),
        },
      ],
    },

    socialLinks: [
      { icon: 'github', link: 'https://github.com/stubbedev/lys-shell' },
    ],

    footer: {
      message: 'Released under the MIT License.',
      copyright: 'Copyright 2026 lys-shell contributors',
    },

    editLink: {
      pattern: 'https://github.com/stubbedev/lys-shell/edit/master/docs/:path',
      text: 'Edit this page on GitHub',
    },

    outline: {
      level: [2, 3],
    },

    search: {
      provider: 'local',
    },
  },
})
