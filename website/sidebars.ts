import type { SidebarsConfig } from '@docusaurus/plugin-content-docs';

// This runs in Node.js - Don't use client-side code here (browser APIs, JSX...)

/**
 * Mirrors the shape of the package: the runtime middleware first (that is
 * what most readers install), then what it detects and how to tune it, the
 * two CLI surfaces, the ORM/driver adapters, and finally the extension seams.
 */
const sidebars: SidebarsConfig = {
  docsSidebar: [
    'intro',
    'getting-started',
    {
      type: 'category',
      label: 'Runtime',
      collapsed: false,
      items: ['middleware', 'n-plus-one', 'noise-control', 'redaction'],
    },
    {
      type: 'category',
      label: 'Rules',
      collapsed: false,
      items: ['rules', 'suppressions', 'configuration'],
    },
    {
      type: 'category',
      label: 'CLI',
      collapsed: false,
      items: ['scan', 'explain'],
    },
    {
      type: 'category',
      label: 'Integrations',
      collapsed: false,
      items: ['integrations', 'gorm', 'sqlx', 'pgx', 'bun', 'xorm', 'ent'],
    },
    {
      type: 'category',
      label: 'Extending',
      collapsed: false,
      items: ['parsers', 'analyzer'],
    },
  ],
};

export default sidebars;
