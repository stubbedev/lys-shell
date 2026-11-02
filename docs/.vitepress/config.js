import { readdirSync } from 'node:fs'
import { lysConfig } from 'lys-docs-theme/config'

const pages = (dir, skip = []) =>
  readdirSync(new URL(`../config/${dir}`, import.meta.url))
    .filter((f) => f.endsWith('.md'))
    .map((f) => f.slice(0, -3))
    .filter((name) => !skip.includes(name))
    .sort()
    .map((name) => ({ text: name, link: `/config/${dir}${name}` }))

export default lysConfig({
  name: 'lys-shell',
  description: 'A configurable desktop shell for Wayland.',
  branch: 'master',
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
          { text: 'Launcher', link: '/guide/launcher' },
          { text: 'CLI', link: '/guide/cli' },
        ],
      },
      {
        text: 'Also see',
        items: [{ text: 'Config reference', link: '/config/' }],
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
})
